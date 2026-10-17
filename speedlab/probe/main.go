// Command probe isolates each stage of the XDL data path to find where
// throughput is lost:
//
//	raw   — bare http.Get + io.Copy(io.Discard): network + TLS + syscall ceiling
//	rawbuf— same but with a 128 KiB copy buffer (what XDL uses)
//	discard-only: no file writes at all
//	file  — XDL's exact pattern: concurrent WriteAt to one preallocated file
//
//	go run ./speedlab/probe -mode rawbuf -size 2147483648
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

func main() {
	mode := flag.String("mode", "rawbuf", "raw|rawbuf|file")
	base := flag.String("url", "http://127.0.0.1:18099/file", "")
	size := flag.Int64("size", 1<<30, "")
	conns := flag.Int("conns", 8, "")
	dir := flag.String("dir", "", "")
	flag.Parse()

	url := fmt.Sprintf("%s/%d", *base, *size)
	tr := &http.Transport{
		DisableCompression: true,
		MaxIdleConns:       64,
		MaxIdleConnsPerHost: 32,
		ForceAttemptHTTP2:  false,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}

	out := *dir
	if out == "" {
		d, _ := os.MkdirTemp("", "xdlprobe")
		out = d
	}
	if *mode == "file" {
		defer os.RemoveAll(out)
		path := filepath.Join(out, "probe.bin")
		if err := prealloc(path, *size); err != nil {
			panic(err)
		}
		defer os.Remove(path)
		start := time.Now()
		if err := runFile(client, url, path, *size, *conns); err != nil {
			fmt.Println("ERR", err)
			return
		}
		report("file WriteAt", *size, start)
		return
	}

	start := time.Now()
	if err := runRaw(client, url, *size, *conns, *mode == "rawbuf"); err != nil {
		fmt.Println("ERR", err)
		return
	}
	name := "raw 4KiB(io.Copy)"
	if *mode == "rawbuf" {
		name = "raw 128KiB bufio"
	}
	report(name, *size, start)
}

func report(name string, size int64, start time.Time) {
	d := time.Since(start)
	fmt.Printf("%-18s %7.1f MiB/s  (%.2fs, %d MiB)\n",
		name, float64(size)/d.Seconds()/(1<<20), d.Seconds(), size>>20)
}

func runRaw(client *http.Client, url string, size int64, n int, bigBuf bool) error {
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seg := size / int64(n)
			from := int64(i) * seg
			to := from + seg - 1
			if i == n-1 {
				to = size - 1
			}
			req, _ := http.NewRequest("GET", url, nil)
			req.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-"+strconv.FormatInt(to, 10))
			resp, err := client.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			if bigBuf {
				buf := make([]byte, 128*1024)
				if _, err := io.CopyBuffer(io.Discard, resp.Body, buf); err != nil {
					errs <- err
				}
			} else if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	return <-errs
}

func runFile(client *http.Client, url, path string, size int64, n int) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			seg := size / int64(n)
			from := int64(i) * seg
			to := from + seg - 1
			if i == n-1 {
				to = size - 1
			}
			req, _ := http.NewRequest("GET", url, nil)
			req.Header.Set("Range", "bytes="+strconv.FormatInt(from, 10)+"-"+strconv.FormatInt(to, 10))
			resp, err := client.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			buf := make([]byte, 128*1024)
			off := from
			for {
				nn, rerr := resp.Body.Read(buf)
				if nn > 0 {
					if _, werr := f.WriteAt(buf[:nn], off); werr != nil {
						errs <- werr
						return
					}
					off += int64(nn)
				}
				if rerr == io.EOF {
					break
				}
				if rerr != nil {
					errs <- rerr
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	return <-errs
}

func prealloc(path string, size int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(size)
}
