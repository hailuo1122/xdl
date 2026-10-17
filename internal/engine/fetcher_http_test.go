package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadRangeMulti(t *testing.T) {
	const size = 2 << 20
	body := make([]byte, size)
	for i := range body {
		body[i] = byte(i % 251)
	}
	want := sha256.Sum256(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Disposition", `attachment; filename="sample.bin"`)
		start, end := 0, len(body)-1
		status := http.StatusOK
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			parts := strings.SplitN(strings.TrimPrefix(rng, "bytes="), "-", 2)
			if parts[0] != "" {
				start, _ = strconv.Atoi(parts[0])
			}
			if len(parts) == 2 && parts[1] != "" {
				end, _ = strconv.Atoi(parts[1])
			}
			if start < 0 || end >= len(body) || start > end {
				http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			status = http.StatusPartialContent
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
		}
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(status)
		_, _ = w.Write(body[start : end+1])
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := NewHTTPDownloader()
	res, err := dl.Download(context.Background(), DownloadOptions{
		URL:   srv.URL + "/sample.bin",
		Dir:   dir,
		Conns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != size {
		t.Fatalf("size %d", len(got))
	}
	sum := sha256.Sum256(got)
	if sum != want {
		t.Fatalf("sha mismatch %s", hex.EncodeToString(sum[:]))
	}
	if filepath.Base(res.Path) != "sample.bin" {
		t.Fatalf("name %s", res.Path)
	}
}

func TestWorkStealDoesNotDuplicateBytes(t *testing.T) {
	const size = 8 << 20 // 8 MiB
	body := make([]byte, size)
	for i := range body {
		body[i] = byte((i*31 + 7) % 251)
	}
	wantSum := sha256.Sum256(body)

	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("ETag", `"steal-test"`)
		start, end := 0, len(body)-1
		status := http.StatusOK
		rng := r.Header.Get("Range")
		if strings.HasPrefix(rng, "bytes=") {
			parts := strings.SplitN(strings.TrimPrefix(rng, "bytes="), "-", 2)
			if parts[0] != "" {
				start, _ = strconv.Atoi(parts[0])
			}
			if len(parts) == 2 && parts[1] != "" {
				end, _ = strconv.Atoi(parts[1])
			}
			if start < 0 || end >= len(body) || start > end {
				http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			status = http.StatusPartialContent
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
		}
		chunk := body[start : end+1]
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(status)

		// Probe is bytes=0-0; first real data chunk (start==0, end>0) is slow.
		bps := int64(8 << 20)
		if start == 0 && end > 0 {
			bps = 512 << 10
		}
		cw := &countingWriter{w: w, n: &served}
		if err := throttleWrite(r.Context(), cw, chunk, bps); err != nil {
			return
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := NewHTTPDownloader()
	start := time.Now()
	res, err := dl.Download(context.Background(), DownloadOptions{
		URL:   srv.URL + "/steal.bin",
		Dir:   dir,
		Name:  "steal.bin",
		Conns: 2,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("elapsed=%s served=%d", elapsed, served.Load())

	const maxServed = size + (512 << 10) // 8 MiB + 512 KiB
	if got := served.Load(); got > maxServed {
		t.Fatalf("served %d bytes, want <= %d (work-steal duplicated data)", got, maxServed)
	}

	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != size {
		t.Fatalf("file size %d want %d", len(got), size)
	}
	sum := sha256.Sum256(got)
	if sum != wantSum {
		t.Fatalf("sha mismatch got=%s want=%s", hex.EncodeToString(sum[:]), hex.EncodeToString(wantSum[:]))
	}
}

type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

func throttleWrite(ctx context.Context, w io.Writer, data []byte, bps int64) error {
	if bps <= 0 {
		_, err := w.Write(data)
		return err
	}
	const step = 16 * 1024
	var sent int
	start := time.Now()
	for sent < len(data) {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := step
		if sent+n > len(data) {
			n = len(data) - sent
		}
		if _, err := w.Write(data[sent : sent+n]); err != nil {
			return err
		}
		sent += n
		// Pace to target bytes/sec.
		want := time.Duration(float64(sent) / float64(bps) * float64(time.Second))
		if sleep := want - time.Since(start); sleep > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleep):
			}
		}
	}
	return nil
}
