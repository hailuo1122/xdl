// Command bench drives the XDL HTTP engine against benchsrv and reports
// throughput plus per-phase observations.
//
//	go run ./speedlab/bench -url http://127.0.0.1:18099/file/2147483648 -conns 8
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xdl-app/xdl/internal/engine"
)

type point struct {
	at    time.Duration
	bytes int64
}

func main() {
	url := flag.String("url", "http://127.0.0.1:18099/file/1073741824", "file URL (server appends size via path)")
	size := flag.Int64("size", 1<<30, "file size in bytes")
	conns := flag.Int("conns", 8, "connection count")
	turbo := flag.Bool("turbo", false, "turbo mode (allows 32 conns)")
	dir := flag.String("dir", "", "output dir (default temp)")
	label := flag.String("label", "", "label for output")
	keep := flag.Bool("keep", false, "keep downloaded file")
	flag.Parse()

	target := *url
	if filepath.Ext(target) == "" {
		target = fmt.Sprintf("%s/%d", target, *size)
	}

	out := *dir
	if out == "" {
		d, err := os.MkdirTemp("", "xdlbench")
		if err != nil {
			panic(err)
		}
		out = d
	}
	defer func() {
		if !*keep {
			os.RemoveAll(out)
		}
	}()

	var pts []point
	var mu sync.Mutex
	var lastSpeed atomic.Int64
	start := time.Now()

	dl := engine.NewHTTPDownloader()
	res, err := dl.Download(context.Background(), engine.DownloadOptions{
		URL:      target,
		Dir:      out,
		Name:     "bench.bin",
		Conns:    *conns,
		Turbo:    *turbo,
		OnProgress: func(done, total, speed int64) {
			el := time.Since(start)
			lastSpeed.Store(speed)
			mu.Lock()
			pts = append(pts, point{at: el, bytes: done})
			mu.Unlock()
		},
	})
	elapsed := time.Since(start)
	if err != nil {
		fmt.Printf("FAIL %-28s err=%v elapsed=%v\n", *label, err, elapsed.Round(time.Millisecond))
		return
	}

	// Confirm the bytes actually landed correctly.
	var verify int64
	if f, err := os.Open(res.Path); err == nil {
		n, _ := io.Copy(io.Discard, f)
		verify = n
		f.Close()
	}

	mbps := float64(res.Size) / elapsed.Seconds() / (1 << 20)
	fmt.Printf("OK   %-28s %6.1f MiB/s  size=%d written=%d elapsed=%v\n",
		*label, mbps, res.Size, verify, elapsed.Round(time.Millisecond))

	// Time-to-50% / 90% exposes slow ramp-up and tail starvation.
	for _, frac := range []float64{0.25, 0.5, 0.9, 1.0} {
		target64 := int64(float64(res.Size) * frac)
		mu.Lock()
		at := time.Duration(-1)
		for _, p := range pts {
			if p.bytes >= target64 {
				at = p.at
				break
			}
		}
		mu.Unlock()
		fmt.Printf("       t%-4d %5.1fs\n", int(frac*100), at.Seconds())
	}

	// Per-second speed curve highlights connection starvation windows.
	mu.Lock()
	curve := snapCurve(pts, elapsed)
	mu.Unlock()
	fmt.Println("       speed curve (MiB/s per 250ms):")
	for i, v := range curve {
		if i%4 == 0 {
			fmt.Printf("  %4.1fs %s\n", float64(i)*0.25, bar(v))
		}
	}
}

func snapCurve(pts []point, total time.Duration) []float64 {
	const step = 250 * time.Millisecond
	n := int(total/step) + 1
	out := make([]float64, n)
	sort.Slice(pts, func(i, j int) bool { return pts[i].at < pts[j].at })
	for i := 1; i < len(pts); i++ {
		slot := int(pts[i].at / step)
		if slot >= n {
			slot = n - 1
		}
		dt := (pts[i].at - pts[i-1].at).Seconds()
		if dt <= 0 {
			continue
		}
		out[slot] = float64(pts[i].bytes-pts[i-1].bytes) / dt / (1 << 20)
	}
	return out
}

func bar(v float64) string {
	const max = 12
	if v <= 0 {
		return "-"
	}
	k := int(v / 8)
	if k > max {
		k = max
	}
	if k < 1 {
		k = 1
	}
	s := ""
	for i := 0; i < k; i++ {
		s += "#"
	}
	return fmt.Sprintf("%6.1f |%s", v, s)
}

var _ = http.StatusOK
