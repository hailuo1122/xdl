package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSourceBankPickPrefersFaster(t *testing.T) {
	b := newSourceBank("http://a/x", []string{"http://b/x", "http://c/x"})
	b.ReportSuccess("http://b/x", 10<<20)
	b.ReportSuccess("http://a/x", 1<<20)
	if got := b.Pick(); got != "http://b/x" {
		t.Fatalf("pick=%s", got)
	}
}

func TestMirrorCompatibleSizeMismatch(t *testing.T) {
	ref := &ProbeResult{Size: 100, AcceptRanges: true, ETag: `"a"`}
	other := &ProbeResult{Size: 99, AcceptRanges: true, ETag: `"a"`}
	if mirrorCompatible(ref, other) {
		t.Fatal("expected size mismatch reject")
	}
}

func TestResolveMirrorsPicksFaster(t *testing.T) {
	body := make([]byte, 2<<20)
	for i := range body {
		body[i] = byte(i)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRange(w, r, body, 40*time.Millisecond)
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRange(w, r, body, 0)
	}))
	defer fast.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	pr, bank, err := resolveMirrors(context.Background(), client, slow.URL+"/f.bin", []string{fast.URL + "/f.bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Size != int64(len(body)) {
		t.Fatalf("size %d", pr.Size)
	}
	if bank.Len() < 2 {
		t.Fatalf("expected both mirrors, got %d", bank.Len())
	}
	// Winner should be the fast mirror's final URL.
	if !strings.Contains(pr.FinalURL, fast.URL) {
		t.Fatalf("expected fast winner, got %s", pr.FinalURL)
	}
}

func serveRange(w http.ResponseWriter, r *http.Request, body []byte, delay time.Duration) {
	if delay > 0 {
		time.Sleep(delay)
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", `"m"`)
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
		if end >= len(body) {
			end = len(body) - 1
		}
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
	}
	w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	w.WriteHeader(status)
	_, _ = w.Write(body[start : end+1])
}
