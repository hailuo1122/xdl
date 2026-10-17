// Command stage compares XDL's engine against an equivalent hand-written
// client on the SAME shaped link, to separate engine overhead from link limits.
//
//	go run ./speedlab/stage -url http://127.0.0.1:18097/file -size 268435456 -conns 16
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
	"strconv"
	"sync"
	"time"

	"github.com/xdl-app/xdl/internal/engine"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:18097/file", "")
	size := flag.Int64("size", 268435456, "")
	conns := flag.Int("conns", 16, "")
	reps := flag.Int("reps", 3, "")
	dir := flag.String("dir", "", "")
	flag.Parse()

	d, _ := os.MkdirTemp("", "xdlstage")
	defer os.RemoveAll(d)
	out := *dir
	if out == "" {
		out = d
	}

	fmt.Println("--- XDL engine ---")
	bench("engine", *reps, func(r int) (float64, error) {
		dl := engine.NewHTTPDownloader()
		start := time.Now()
		_, err := dl.Download(context.Background(), engine.DownloadOptions{
			URL:   fmt.Sprintf("%s/%d", *base, *size),
			Dir:   out,
			Name:  fmt.Sprintf("e%d.bin", r),
			Conns: *conns,
		})
		if err != nil {
			return 0, err
		}
		return float64(*size) / time.Since(start).Seconds() / (1 << 20), nil
	})

	fmt.Println("--- hand-written equivalent (all workers at once, no ramp-up) ---")
	bench("manual-now", *reps, func(r int) (float64, error) {
		return manual(fmt.Sprintf("%s/%d", *base, *size), *size, *conns, filepath.Join(out, fmt.Sprintf("m%d.bin", r)), true)
	})

	fmt.Println("--- hand-written with XDL's 2s ramp-up ---")
	bench("manual-ramp", *reps, func(r int) (float64, error) {
		return manual(fmt.Sprintf("%s/%d", *base, *size), *size, *conns, filepath.Join(out, fmt.Sprintf("r%d.bin", r)), false)
	})

	fmt.Println("--- manual, discard (no disk writes) ---")
	bench("manual-nodisk", *reps, func(r int) (float64, error) {
		start := time.Now()
		err := manualStream(fmt.Sprintf("%s/%d", *base, *size), *size, *conns, true)
		if err != nil {
			return 0, err
		}
		return float64(*size) / time.Since(start).Seconds() / (1 << 20), nil
	})
}

func bench(label string, reps int, fn func(int) (float64, error)) {
	var v []float64
	for r := 0; r < reps; r++ {
		m, err := fn(r)
		if err != nil {
			fmt.Printf("  %-14s FAIL %v\n", label, err)
			return
		}
		v = append(v, m)
	}
	sort.Float64s(v)
	fmt.Printf("  %-14s median=%7.1f MiB/s  (min %.1f max %.1f)\n", label, v[len(v)/2], v[0], v[len(v)-1])
}

func manual(base string, size int64, n int, path string, allAtOnce bool) (float64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { f.Close(); os.Remove(path) }()
	if err := f.Truncate(size); err != nil {
		return 0, err
	}

	start := time.Now()
	seg := size / int64(n)
	if size%int64(n) != 0 {
		seg = (size + int64(n) - 1) / int64(n)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	var launch sync.WaitGroup
	started := 0
	var mu sync.Mutex

	do := func(i int) {
		mu.Lock()
		started++
		mu.Unlock()
		from := int64(i) * seg
		if from >= size {
			wg.Done()
			return
		}
		to := from + seg - 1
		if to > size-1 {
			to = size - 1
		}
		buf := make([]byte, 128*1024)
		req, _ := http.NewRequest("GET", base, nil)
		req.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-"+strconv.FormatInt(to, 10))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			wg.Done()
			return
		}
		defer resp.Body.Close()
		off := from
		for {
			nn, rerr := resp.Body.Read(buf)
			if nn > 0 {
				if _, werr := f.WriteAt(buf[:nn], off); werr != nil {
					errCh <- werr
					wg.Done()
					return
				}
				off += int64(nn)
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				errCh <- rerr
				wg.Done()
				return
			}
		}
		wg.Done()
	}

	wg.Add(n)
	startN := n
	if !allAtOnce && startN > 4 {
		startN = 4
	}
	for i := 0; i < startN; i++ {
		go do(i)
	}
	if startN < n {
		launch.Add(1)
		go func() {
			defer launch.Done()
			for i := startN; i < n; i++ {
				time.Sleep(2 * time.Second)
				go do(i)
			}
		}()
	}
	wg.Wait()
	launch.Wait()
	select {
	case err := <-errCh:
		return 0, err
	default:
	}
	return float64(size) / time.Since(start).Seconds() / (1 << 20), nil
}

func manualStream(url string, size int64, n int, allAtOnce bool) error {
	seg := size / int64(n)
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			from := int64(i) * seg
			to := from + seg - 1
			if i == n-1 {
				to = size - 1
			}
			req, _ := http.NewRequest("GET", url, nil)
			req.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-"+strconv.FormatInt(to, 10))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()
			buf := make([]byte, 128*1024)
			for {
				_, rerr := resp.Body.Read(buf)
				if rerr == io.EOF {
					return
				}
				if rerr != nil {
					errCh <- rerr
					return
				}
			}
		}(i)
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}
	return nil
}
