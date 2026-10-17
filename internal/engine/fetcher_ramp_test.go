package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newRangeServer serves /f/{size} with byte-range support and records the peak
// number of simultaneously open requests.
func newRangeServer(t *testing.T, size int64) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var inflight atomic.Int32
	var peak atomic.Int64
	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if int64(n) <= p || peak.CompareAndSwap(p, int64(n)) {
				break
			}
		}
		defer inflight.Add(-1)

		s, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/f/"), 10, 64)
		if s <= 0 {
			s = size
		}
		w.Header().Set("ETag", `"ramp"`)
		w.Header().Set("Accept-Ranges", "bytes")
		start, end := int64(0), s-1
		st := http.StatusOK
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			parts := strings.SplitN(strings.TrimPrefix(rng, "bytes="), "-", 2)
			if len(parts) == 2 {
				if parts[0] != "" {
					start, _ = strconv.ParseInt(parts[0], 10, 64)
				}
				if parts[1] != "" {
					end, _ = strconv.ParseInt(parts[1], 10, 64)
				}
				if end > s-1 {
					end = s - 1
				}
				st = http.StatusPartialContent
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, s))
			}
		}
		length := end - start + 1
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.WriteHeader(st)
		if r.Method == http.MethodHead {
			return
		}
		buf := make([]byte, 32*1024)
		left := length
		for left > 0 {
			k := int64(len(buf))
			if k > left {
				k = left
			}
			if _, err := w.Write(buf[:k]); err != nil {
				return
			}
			served.Add(k)
			left -= k
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &peak
}

// TestConnectionsOpenImmediately is the regression guard for the removed 2s
// ramp-up. All configured connections must be in flight right away; the old
// code held concurrency at 4 and added one every 2s, so reaching width 8 took
// 8s and width 32 (turbo) took 56s.
func TestConnectionsOpenImmediately(t *testing.T) {
	const size = 64 << 20
	srv, peak := newRangeServer(t, size)
	dl := NewHTTPDownloader()

	if _, err := dl.Download(context.Background(), DownloadOptions{
		URL:   srv.URL + "/f/67108864",
		Dir:   t.TempDir(),
		Name:  "width.bin",
		Conns: 8,
	}); err != nil {
		t.Fatalf("download: %v", err)
	}

	if got := peak.Load(); got < 8 {
		t.Fatalf("peak concurrent requests = %d, want >= 8 (connections are not opened at once)", got)
	}
}

// TestFullWidthReachedFast measures how quickly the download achieves the
// configured connection width, which is what the ramp-up used to delay.
func TestFullWidthReachedFast(t *testing.T) {
	srv, _ := newRangeServer(t, 128<<20)
	dl := NewHTTPDownloader()

	start := time.Now()
	res, err := dl.Download(context.Background(), DownloadOptions{
		URL:   srv.URL + "/f/134217728",
		Dir:   t.TempDir(),
		Name:  "width.bin",
		Conns: 8,
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	el := time.Since(start)
	mbps := float64(res.Size) / el.Seconds() / (1 << 20)
	t.Logf("128MiB with 8 conns: %.1f MiB/s in %v", mbps, el.Round(time.Millisecond))
	// With the old 2s ramp the last workers started at t=8s, so any file that
	// finishes quickly was effectively downloaded by 4 connections only.
	if el > 3*time.Second {
		t.Fatalf("download took %v; expected well under the old 8s ramp floor", el)
	}
}

// TestGateHonoursRetryAfter verifies the 429 cool-off gate pauses workers.
func TestGateHonoursRetryAfter(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if hits.Load() <= 2 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		s, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/f/"), 10, 64)
		if s <= 0 {
			s = 1 << 20
		}
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", s-1, s))
		w.Header().Set("Content-Length", strconv.FormatInt(s, 10))
		w.WriteHeader(http.StatusPartialContent)
		buf := make([]byte, 32*1024)
		left := s
		for left > 0 {
			k := int64(len(buf))
			if k > left {
				k = left
			}
			if _, err := w.Write(buf[:k]); err != nil {
				return
			}
			left -= k
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	g := newGate()
	g.trip(120 * time.Millisecond)
	start := time.Now()
	g.wait(context.Background())
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Fatalf("gate returned after %v, expected the cool-off to be honoured", el)
	}

	// A nil gate must be inert.
	var nilGate *gate
	nilGate.trip(time.Second)
	nilGate.wait(context.Background())
}

// TestGateKeepsLongestCoolOff ensures concurrent 429s extend, not shrink, the wait.
func TestGateKeepsLongestCoolOff(t *testing.T) {
	g := newGate()
	g.trip(80 * time.Millisecond)
	g.trip(10 * time.Millisecond)
	start := time.Now()
	g.wait(context.Background())
	if el := time.Since(start); el < 70*time.Millisecond {
		t.Fatalf("gate shortened to %v; a later smaller trip must not shrink the cool-off", el)
	}
	// Second wait must be immediate: the cool-off is consumed once.
	start = time.Now()
	g.wait(context.Background())
	if el := time.Since(start); el > 20*time.Millisecond {
		t.Fatalf("gate re-armed for %v; cool-off should be consumed after one wait", el)
	}
}

// TestGateTripClampsPanicValue ensures a hostile Retry-After cannot stall a task.
func TestGateTripClampsPanicValue(t *testing.T) {
	g := newGate()
	g.trip(24 * time.Hour)
	g.mu.Lock()
	got := g.cooldown
	g.mu.Unlock()
	if got > 60*time.Second {
		t.Fatalf("cool-off %v exceeds the 60s clamp", got)
	}
}
