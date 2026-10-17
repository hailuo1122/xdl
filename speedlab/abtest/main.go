// Command abtest measures XDL end-to-end throughput under a shaped link, so
// disk noise cannot dominate the result. The server throttles both the shared
// link and each connection; the client is the real engine.
//
//	go run ./speedlab/abtest -url http://127.0.0.1:18098/file -size 536870912
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/xdl-app/xdl/internal/engine"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:18098/file", "")
	size := flag.Int64("size", 512<<20, "")
	conns := flag.Int("conns", 8, "")
	reps := flag.Int("reps", 3, "")
	label := flag.String("label", "run", "")
	dir := flag.String("dir", "", "")
	flag.Parse()

	d, _ := os.MkdirTemp("", "xdlab")
	defer os.RemoveAll(d)
	target := *dir
	if target == "" {
		target = d
	}

	var runs []float64
	var t50s []float64
	for r := 0; r < *reps; r++ {
		var t50 time.Duration
		start := time.Now()
		dl := engine.NewHTTPDownloader()
		_, err := dl.Download(context.Background(), engine.DownloadOptions{
			URL:   fmt.Sprintf("%s/%d", *base, *size),
			Dir:   target,
			Name:  fmt.Sprintf("ab%d.bin", r),
			Conns: *conns,
			OnProgress: func(done, total, speed int64) {
				if t50 == 0 && total > 0 && done >= total/2 {
					t50 = time.Since(start)
				}
			},
		})
		el := time.Since(start)
		if err != nil {
			fmt.Printf("FAIL %s rep=%d err=%v\n", *label, r, err)
			return
		}
		runs = append(runs, float64(*size)/el.Seconds()/(1<<20))
		t50s = append(t50s, t50.Seconds())
	}
	sort.Float64s(runs)
	sort.Float64s(t50s)
	fmt.Printf("%-26s median=%7.1f MiB/s  min=%7.1f  max=%7.1f  t50=%.1fs\n",
		*label, med(runs), runs[0], runs[len(runs)-1], t50s[len(t50s)/2])
}

func med(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return v[len(v)/2]
}
