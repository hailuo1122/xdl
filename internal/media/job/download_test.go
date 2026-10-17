package job

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadHLSPlain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/seg/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("SEGDATA-" + r.URL.Path))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	dl := NewDownloader()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := dl.Download(ctx, Options{
		URL:  srv.URL + "/media.m3u8",
		Dir:  dir,
		Name: "out.ts",
		Meta: Meta{Protocol: "hls"},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 10 {
		t.Fatalf("size=%d", st.Size())
	}
}

func TestDownloadHLSAES(t *testing.T) {
	key := []byte("0123456789abcdef")
	encrypt := func(plain []byte, seq uint64) []byte {
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
		binary.BigEndian.PutUint64(iv[8:], seq)
		block, _ := aes.NewCipher(key)
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, out)
		return out
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/media.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-KEY:METHOD=AES-128,URI="key.bin"
#EXTINF:4.0,
0.ts
#EXTINF:4.0,
1.ts
#EXT-X-ENDLIST
`)
	})
	mux.HandleFunc("/key.bin", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(key)
	})
	mux.HandleFunc("/0.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encrypt([]byte("hello-seg-0-plain-data!!!!!!"), 0))
	})
	mux.HandleFunc("/1.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encrypt([]byte("hello-seg-1-plain-data!!!!!!"), 1))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	dl := NewDownloader()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := dl.Download(ctx, Options{
		URL:  srv.URL + "/media.m3u8",
		Dir:  dir,
		Name: "aes.ts",
		Meta: Meta{Protocol: "hls"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("hello-seg-0")) || !bytes.Contains(body, []byte("hello-seg-1")) {
		t.Fatalf("decrypted output missing plaintext: %q", body[:min(80, len(body))])
	}
}

func TestDownloadDASH(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.mpd", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?>
<MPD type="static" mediaPresentationDuration="PT4S" minBufferTime="PT1S">
  <Period>
    <AdaptationSet contentType="video" mimeType="video/mp4">
      <Representation id="1" bandwidth="800000" width="640" height="360">
        <SegmentList>
          <Initialization sourceURL="init.mp4"/>
          <SegmentURL media="s0.m4s"/>
          <SegmentURL media="s1.m4s"/>
        </SegmentList>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`)
	})
	mux.HandleFunc("/init.mp4", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("INIT")) })
	mux.HandleFunc("/s0.m4s", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("S0")) })
	mux.HandleFunc("/s1.m4s", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("S1")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	dl := NewDownloader()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := dl.Download(ctx, Options{
		URL:  srv.URL + "/manifest.mpd",
		Dir:  dir,
		Name: "dash.mp4",
		Meta: Meta{Protocol: "dash", VariantID: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "INITS0S1" {
		t.Fatalf("got %q", body)
	}
	_ = filepath.Base(res.Path)
}

func TestProbeRejectsDRM(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/drm.mpd", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?>
<MPD type="static" mediaPresentationDuration="PT4S">
  <Period>
    <AdaptationSet contentType="video">
      <ContentProtection schemeIdUri="urn:uuid:edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"/>
      <Representation id="1" bandwidth="1"><BaseURL>a.mp4</BaseURL></Representation>
    </AdaptationSet>
  </Period>
</MPD>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_, err := Probe(context.Background(), srv.URL+"/drm.mpd", nil)
	if err == nil {
		t.Fatal("expected DRM reject")
	}
}

