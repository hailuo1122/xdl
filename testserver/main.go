package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Scenario test server for XDL engine validation.
// Endpoints:
//   /file/{size}          — Range-capable file of given bytes (filled with pattern)
//   /norange/{size}       — ignores Range, always 200
//   /slow/{bps}/{size}    — per-connection throttle
//   /429/{size}           — first N requests 429, then OK
//   /etag-flip/{size}     — changes ETag mid-flight after ?flip=1
//   /sha256/{size}        — returns X-Content-SHA256 header
//   /m2/page              — browser takeover test page
//   /m2/guarded/{size}    — requires xdl_test cookie + /m2/page referer
//   /m2/attach/{size}     — attachment without link extension (downloads takeover)
//   /m2/video.mp4         — tiny video/mp4 sample
//   /m2/hls.m3u8          — VOD playlist for media sniffing
//   /m3/master.m3u8       — HLS master with 2 variants
//   /m3/media_low.m3u8    — HLS media (TS segments)
//   /m3/media_hi.m3u8
//   /m3/seg/{n}.ts        — tiny TS-like payload
//   /m3/aes/media.m3u8    — AES-128 VOD
//   /m3/aes/key.bin
//   /m3/aes/seg/{n}.ts    — encrypted segments
//   /m3/manifest.mpd      — static DASH VOD (SegmentList)
//   /m3/dash/init.mp4
//   /m3/dash/seg/{n}.m4s
//   /m3/drm.mpd           — ContentProtection (must reject)
//   /m3/live.m3u8         — live playlist (must reject)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	flag.Parse()

	var hits429 atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/file/", func(w http.ResponseWriter, r *http.Request) {
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/file/"))
		serveRange(w, r, size, true, 0, "test-etag-"+strconv.FormatInt(size, 10))
	})
	mux.HandleFunc("/norange/", func(w http.ResponseWriter, r *http.Request) {
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/norange/"))
		serveRange(w, r, size, false, 0, "")
	})
	mux.HandleFunc("/slow/", func(w http.ResponseWriter, r *http.Request) {
		// /slow/{bps}/{size}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/slow/"), "/")
		if len(parts) < 2 {
			http.Error(w, "bad path", 400)
			return
		}
		bps, _ := strconv.ParseInt(parts[0], 10, 64)
		size := parseSize(parts[1])
		serveRange(w, r, size, true, bps, "slow-etag")
	})
	mux.HandleFunc("/429/", func(w http.ResponseWriter, r *http.Request) {
		n := hits429.Add(1)
		if n <= 3 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many", 429)
			return
		}
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/429/"))
		serveRange(w, r, size, true, 0, "429-etag")
	})
	mux.HandleFunc("/sha256/", func(w http.ResponseWriter, r *http.Request) {
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/sha256/"))
		sum := contentSHA(size)
		w.Header().Set("X-Content-SHA256", sum)
		serveRange(w, r, size, true, 0, "sha-"+sum[:8])
	})
	mux.HandleFunc("/m2/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, m2PageHTML)
	})
	mux.HandleFunc("/m2/auth/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xdl-e2e" {
			http.Error(w, "auth: missing bearer", http.StatusUnauthorized)
			return
		}
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/m2/auth/"))
		serveRange(w, r, size, true, 0, "auth-"+strconv.FormatInt(size, 10))
	})
	mux.HandleFunc("/m2/headfail/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			http.Error(w, "HEAD not supported", http.StatusMethodNotAllowed)
			return
		}
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/m2/headfail/"))
		serveRange(w, r, size, true, 0, "headfail-"+strconv.FormatInt(size, 10))
	})
	mux.HandleFunc("/m2/set-chips", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:        "xdl_chips",
			Value:       "1",
			Path:        "/",
			Secure:      true,
			SameSite:    http.SameSiteNoneMode,
			Partitioned: true,
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><title>chips</title><p>partitioned cookie set</p>")
	})
	mux.HandleFunc("/m2/chips-page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, m2ChipsHTML)
	})
	mux.HandleFunc("/m2/chips/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "xdl_chips=1") {
			http.Error(w, "chips: missing partitioned cookie", http.StatusForbidden)
			return
		}
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/m2/chips/"))
		serveRange(w, r, size, true, 0, "chips-"+strconv.FormatInt(size, 10))
	})
	mux.HandleFunc("/m2/guarded/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "xdl_test=1") || !strings.Contains(r.Header.Get("Referer"), "/m2/page") {
			http.Error(w, "guarded: missing cookie or referer", http.StatusForbidden)
			return
		}
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/m2/guarded/"))
		serveRange(w, r, size, true, 0, "guarded-"+strconv.FormatInt(size, 10))
	})
	mux.HandleFunc("/m2/attach/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "xdl_test=1") || !strings.Contains(r.Header.Get("Referer"), "/m2/page") {
			http.Error(w, "guarded: missing cookie or referer", http.StatusForbidden)
			return
		}
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/m2/attach/"))
		w.Header().Set("Content-Disposition", `attachment; filename="m2-auto.bin"`)
		serveRange(w, r, size, true, 0, "auto-"+strconv.FormatInt(size, 10))
	})
	mux.HandleFunc("/m2/slowattach/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "xdl_test=1") || !strings.Contains(r.Header.Get("Referer"), "/m2/page") {
			http.Error(w, "guarded: missing cookie or referer", http.StatusForbidden)
			return
		}
		size := parseSize(strings.TrimPrefix(r.URL.Path, "/m2/slowattach/"))
		// Slow enough that chrome.downloads.pause can reliably catch it, fast
		// enough that a resumed fallback finishes within a few seconds.
		serveRange(w, r, size, true, 512*1024, "slowattach-"+strconv.FormatInt(size, 10))
	})
	mux.HandleFunc("/m2/video.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "4096")
		w.Header().Set("Accept-Ranges", "bytes")
		body := make([]byte, 4096)
		fillPattern(body, 0)
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/m2/hls.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	})

	// --- M3 media fixtures ---
	mux.HandleFunc("/m3/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360
media_low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1280x720
media_hi.m3u8
`)
	})
	mux.HandleFunc("/m3/media_low.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:4.0,
seg/0.ts
#EXTINF:4.0,
seg/1.ts
#EXT-X-ENDLIST
`)
	})
	mux.HandleFunc("/m3/media_hi.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:4.0,
seg/0.ts
#EXTINF:4.0,
seg/1.ts
#EXTINF:4.0,
seg/2.ts
#EXT-X-ENDLIST
`)
	})
	mux.HandleFunc("/m3/seg/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		n := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/m3/seg/"), ".ts")
		body := make([]byte, 4096)
		fillPattern(body, int64(parseSize(n)))
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/m3/live.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\nseg/0.ts\n")
	})
	aesKey := []byte("0123456789abcdef")
	mux.HandleFunc("/m3/aes/key.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(aesKey)
	})
	mux.HandleFunc("/m3/aes/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-KEY:METHOD=AES-128,URI="key.bin"
#EXTINF:4.0,
seg/0.ts
#EXTINF:4.0,
seg/1.ts
#EXT-X-ENDLIST
`)
	})
	mux.HandleFunc("/m3/aes/seg/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		n := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/m3/aes/seg/"), ".ts")
		plain := make([]byte, 4096)
		fillPattern(plain, int64(parseSize(n)))
		enc, err := aesEncryptCBC(aesKey, plain, uint64(parseSize(n)))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, _ = w.Write(enc)
	})
	mux.HandleFunc("/m3/manifest.mpd", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/dash+xml")
		fmt.Fprint(w, `<?xml version="1.0"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT8S" minBufferTime="PT1S" profiles="urn:mpeg:dash:profile:isoff-on-demand:2011">
  <Period>
    <AdaptationSet contentType="video" mimeType="video/mp4">
      <Representation id="360" bandwidth="800000" width="640" height="360">
        <SegmentList>
          <Initialization sourceURL="dash/init.mp4"/>
          <SegmentURL media="dash/seg/0.m4s"/>
          <SegmentURL media="dash/seg/1.m4s"/>
        </SegmentList>
      </Representation>
      <Representation id="720" bandwidth="2400000" width="1280" height="720">
        <SegmentList>
          <Initialization sourceURL="dash/init.mp4"/>
          <SegmentURL media="dash/seg/0.m4s"/>
          <SegmentURL media="dash/seg/1.m4s"/>
          <SegmentURL media="dash/seg/2.m4s"/>
        </SegmentList>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`)
	})
	mux.HandleFunc("/m3/dash/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		body := make([]byte, 2048)
		fillPattern(body, int64(len(r.URL.Path)))
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/m3/drm.mpd", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/dash+xml")
		fmt.Fprint(w, `<?xml version="1.0"?>
<MPD type="static" mediaPresentationDuration="PT8S" minBufferTime="PT1S">
  <Period>
    <AdaptationSet contentType="video" mimeType="video/mp4">
      <ContentProtection schemeIdUri="urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"/>
      <Representation id="1" bandwidth="800000" width="640" height="360">
        <BaseURL>dash/init.mp4</BaseURL>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`)
	})

	fmt.Println("testserver listening on", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		panic(err)
	}
}

func parseSize(s string) int64 {
	s = strings.TrimSuffix(s, "/")
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 1024
	}
	return n
}

func serveRange(w http.ResponseWriter, r *http.Request, size int64, allowRange bool, bps int64, etag string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Encoding", "identity")
	if etag != "" {
		w.Header().Set("ETag", `"`+etag+`"`)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="test-`+strconv.FormatInt(size, 10)+`.bin"`)

	start, end := int64(0), size-1
	status := http.StatusOK

	if allowRange {
		w.Header().Set("Accept-Ranges", "bytes")
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
				if start > end || start < 0 {
					http.Error(w, "unsatisfiable", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				status = http.StatusPartialContent
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			}
		}
	}

	length := end - start + 1
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}

	const chunk = 16 * 1024
	buf := make([]byte, chunk)
	remaining := length
	offset := start
	for remaining > 0 {
		n := int64(chunk)
		if n > remaining {
			n = remaining
		}
		fillPattern(buf[:n], offset)
		if bps > 0 {
			// throttle: sleep proportionally
			sleep := time.Duration(float64(n) / float64(bps) * float64(time.Second))
			if sleep > 0 {
				time.Sleep(sleep)
			}
		}
		if _, err := w.Write(buf[:n]); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		remaining -= n
		offset += n
	}
}

func fillPattern(b []byte, offset int64) {
	for i := range b {
		b[i] = byte((offset + int64(i)) % 251)
	}
}

func contentSHA(size int64) string {
	h := sha256.New()
	buf := make([]byte, 64*1024)
	var off int64
	for off < size {
		n := int64(len(buf))
		if off+n > size {
			n = size - off
		}
		fillPattern(buf[:n], off)
		h.Write(buf[:n])
		off += n
	}
	return hex.EncodeToString(h.Sum(nil))
}

func aesEncryptCBC(key, plain []byte, mediaSeq uint64) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	if pad == 0 {
		pad = aes.BlockSize
	}
	out := make([]byte, len(plain)+pad)
	copy(out, plain)
	for i := len(plain); i < len(out); i++ {
		out[i] = byte(pad)
	}
	iv := make([]byte, 16)
	binary.BigEndian.PutUint64(iv[8:], mediaSeq)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, out)
	return out, nil
}

const m2PageHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><link rel="icon" href="data:,"><title>XDL M2 takeover test</title>
<style>body{font-family:system-ui;background:#111;color:#eee;padding:40px;line-height:2}a{color:#e6a23c;display:inline-block}video{width:320px;display:block;margin-top:20px;border:1px solid #333}</style>
</head><body>
<h1>XDL browser takeover</h1>
<p>Cookie + Referer protected downloads. If XDL forwards context, files land in its folder.</p>
<a id="guarded" href="/m2/guarded/3145728" download="m2-guarded.bin">guarded click download (3 MiB)</a><br>
<a id="direct" href="/m2/guarded/2097152" download="m2-direct.bin">guarded direct download (2 MiB)</a><br>
<a id="plain" href="/file/5242880" download="m2-plain.bin">plain download (5 MiB)</a><br>
<a id="small" href="/file/524288" download="m2-small.bin">small download (0.5 MiB)</a><br>
<a id="attach" href="/m2/attach/2621440">attachment without extension (2.5 MiB)</a><br>
<a id="slowattach" href="/m2/slowattach/2621440">slow attachment for ask mode (2.5 MiB)</a><br>
<a id="slowattach2" href="/m2/slowattach/2621441">slow attachment 2</a><br>
<a id="slowattach3" href="/m2/slowattach/2621442">slow attachment 3</a><br>
<a id="auth" href="/m2/auth/2097152" download="m2-auth.bin">Authorization protected (2 MiB)</a><br>
<a id="hls" href="/m2/hls.m3u8">HLS playlist</a><br>
<a id="headfail" href="/m2/headfail/1310720" download="m2-headfail.bin">HEAD 405 (1.25 MiB)</a>
<video src="/m2/video.mp4" controls></video>
<script>
fetch('/m2/auth/2097152', {method:'HEAD', headers:{Authorization:'Bearer xdl-e2e'}}).catch(() => {});
</script>
</body></html>`

const m2ChipsHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><link rel="icon" href="data:,"><title>XDL CHIPS test</title></head>
<body>
<iframe src="http://127.0.0.1:18080/m2/set-chips" style="width:1px;height:1px" title="chips"></iframe>
<p><a id="chips" href="http://127.0.0.1:18080/m2/chips/2097152" download="m2-chips.bin">partitioned cookie download (2 MiB)</a></p>
</body></html>`
