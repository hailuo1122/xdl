// Command benchsrv is a fast, optionally throttled Range-capable server used
// to benchmark the XDL download engine.
//
//	/file/{bytes}      Range-capable
//	/norange/{bytes}   ignores Range, always 200
//
// Throttling models a WAN link: -cap is a shared link budget, -perconn caps
// each individual connection. Together they let us reproduce the situation
// where a single TCP stream cannot saturate the link but N streams can.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pacer is a strict "next allowed send time" pacer.
type pacer struct {
	mu   sync.Mutex
	rate float64 // bytes per second; 0 means unlimited
	next time.Time
}

func newPacer(bps float64) *pacer {
	if bps <= 0 {
		return nil
	}
	return &pacer{rate: bps, next: time.Now()}
}

// wait blocks until n more bytes are allowed onto the wire.
func (p *pacer) wait(n int) {
	if p == nil || n <= 0 {
		return
	}
	d := time.Duration(float64(n) / p.rate * float64(time.Second))
	p.mu.Lock()
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	p.next = p.next.Add(d)
	target := p.next
	p.mu.Unlock()
	if sleep := time.Until(target); sleep > 0 {
		time.Sleep(sleep)
	}
}

var (
	pattern  []byte
	globalCap *pacer
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18099", "listen address")
	bufsz := flag.Int("bufsz", 8<<20, "pattern buffer bytes")
	capBps := flag.Float64("cap", 0, "global link budget bytes/sec (0 = unlimited)")
	perConn := flag.Float64("perconn", 0, "per-connection bytes/sec (0 = unlimited)")
	flag.Parse()

	pattern = make([]byte, *bufsz)
	x := uint32(2654435761)
	for i := range pattern {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		pattern[i] = byte(x)
	}
	globalCap = newPacer(*capBps)

	slice := len(pattern)
	if *capBps > 0 || *perConn > 0 {
		slice = 1 << 20 // coarser writes keep the pacer from being sleep-bound
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/file/", func(w http.ResponseWriter, r *http.Request) {
		// A fresh per-connection pacer per request: perConn must bound each
		// stream independently, otherwise N connections collapse into one.
		serve(w, r, parseSize(strings.TrimPrefix(r.URL.Path, "/file/")), true, newPacer(*perConn), slice)
	})
	mux.HandleFunc("/norange/", func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, parseSize(strings.TrimPrefix(r.URL.Path, "/norange/")), false, newPacer(*perConn), slice)
	})

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(os.Stdout, "benchsrv addr=%s bufsz=%d cap=%.0f perconn=%.0f slice=%d\n",
		*addr, *bufsz, *capBps, *perConn, slice)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseSize(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(s), "/"), 10, 64)
	if err != nil || n <= 0 {
		return 1 << 20
	}
	return n
}

func serve(w http.ResponseWriter, r *http.Request, size int64, allowRange bool, pc *pacer, slice int) {
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("ETag", `"bench-fixed"`)

	start, end := int64(0), size-1
	status := http.StatusOK

	if allowRange {
		h.Set("Accept-Ranges", "bytes")
		if rng := r.Header.Get("Range"); rng != "" && strings.HasPrefix(rng, "bytes=") {
			spec := strings.TrimPrefix(rng, "bytes=")
			parts := strings.SplitN(spec, "-", 2)
			if len(parts) == 2 {
				if parts[0] != "" {
					start, _ = strconv.ParseInt(parts[0], 10, 64)
				}
				if parts[1] != "" {
					end, _ = strconv.ParseInt(parts[1], 10, 64)
				}
				if end >= size {
					end = size - 1
				}
				if start < 0 || start > end {
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				status = http.StatusPartialContent
				h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			}
		}
	}

	n := end - start + 1
	h.Set("Content-Length", strconv.FormatInt(n, 10))
	w.WriteHeader(status)

	for off, left := start, n; left > 0; {
		c := int64(slice)
		if c > left {
			c = left
		}
		globalCap.wait(int(c))
		pc.wait(int(c))
		if _, err := writeSlice(w, off, c); err != nil {
			return
		}
		off += c
		left -= c
	}
}

// writeSlice emits n bytes of the repeating pattern starting at logical offset.
func writeSlice(w http.ResponseWriter, off, n int64) (int64, error) {
	total := int64(len(pattern))
	var written int64
	for written < n {
		o := (off + written) % total
		c := n - written
		if rem := total - o; c > rem {
			c = rem
		}
		nn, err := w.Write(pattern[o : o+c])
		written += int64(nn)
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
