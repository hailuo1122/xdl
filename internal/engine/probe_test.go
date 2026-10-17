package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xdl-app/xdl/internal/netx"
)

func TestProbeRetries429(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := n.Add(1)
		if i < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Range", "bytes 0-0/128")
		w.Header().Set("Content-Length", "1")
		w.Header().Set("ETag", `"ok"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	client := netx.NewProbeClient(5 * time.Second)
	pr, err := Probe(context.Background(), client, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Size != 128 || !pr.AcceptRanges {
		t.Fatalf("probe %+v", pr)
	}
	if got := n.Load(); got != 3 {
		t.Fatalf("requests %d want 3", got)
	}
}

func TestProbe404NoRetry(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := netx.NewProbeClient(5 * time.Second)
	_, err := Probe(context.Background(), client, srv.URL, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	te, ok := err.(*TaskError)
	if !ok || te.Code != ErrCodeNotFound {
		t.Fatalf("err %v", err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("requests %d want 1", got)
	}
}

func TestRejectMismatchedContentRange(t *testing.T) {
	const size = 8 << 20
	body := make([]byte, size)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "bytes=0-0" || rng == "" {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("ETag", `"bad-range"`)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", size))
			w.Header().Set("Content-Length", "1")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[:1])
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 1-2/%d", size))
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("no"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := NewHTTPDownloader()
	res, err := dl.Download(context.Background(), DownloadOptions{
		URL:   srv.URL + "/bad.bin",
		Dir:   dir,
		Name:  "bad.bin",
		Conns: 1,
	})
	if err == nil {
		t.Fatalf("expected failure, got %+v", res)
	}
	if res != nil {
		t.Fatal("result should be nil")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), partSuffix) {
			t.Fatalf("final file created: %s", filepath.Join(dir, e.Name()))
		}
	}
}

func TestSingleStreamShortBodyFails(t *testing.T) {
	const declared = 64 * 1024
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", declared))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := NewHTTPDownloader()
	res, err := dl.Download(context.Background(), DownloadOptions{
		URL:   srv.URL + "/short.bin",
		Dir:   dir,
		Name:  "short.bin",
		Conns: 1,
	})
	if err == nil {
		t.Fatalf("expected failure, got %+v", res)
	}
	final := filepath.Join(dir, "short.bin")
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("final file should not exist: %v", err)
	}
	part := final + partSuffix
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("partial file should remain: %v", err)
	}
}
