package hls

import (
	"strings"
	"testing"
)

const vod = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:10
#EXTINF:10.0,
seg0.ts
#EXTINF:9.0,
seg1.ts
#EXT-X-ENDLIST
`

const live = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXTINF:6.0,
seg0.ts
`

const sampleAES = `#EXTM3U
#EXT-X-VERSION:5
#EXT-X-TARGETDURATION:10
#EXT-X-KEY:METHOD=SAMPLE-AES,URI="skd://key"
#EXTINF:10.0,
seg0.ts
#EXT-X-ENDLIST
`

func TestParseVOD(t *testing.T) {
	m, err := ParseMedia(strings.NewReader(vod))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Segments) != 2 {
		t.Fatalf("segments=%d", len(m.Segments))
	}
	if m.Segments[0].URI != "seg0.ts" {
		t.Fatal(m.Segments[0].URI)
	}
}

func TestRejectLive(t *testing.T) {
	_, err := ParseMedia(strings.NewReader(live))
	if err == nil {
		t.Fatal("expected live rejection")
	}
}

func TestRejectSampleAES(t *testing.T) {
	_, err := ParseMedia(strings.NewReader(sampleAES))
	if err == nil {
		t.Fatal("expected sample-aes rejection")
	}
}

const master = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2400000,RESOLUTION=1280x720
hi.m3u8
`

func TestParseMaster(t *testing.T) {
	pl, err := Parse(strings.NewReader(master))
	if err != nil {
		t.Fatal(err)
	}
	if !pl.IsMaster || len(pl.Master) != 2 {
		t.Fatalf("%+v", pl)
	}
	if pl.Master[1].Label() != "1280x720" {
		t.Fatal(pl.Master[1].Label())
	}
}
