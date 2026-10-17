package hls

import (
	"fmt"
	"io"
	"strings"

	"github.com/grafov/m3u8"
)

// Media is a VOD media playlist. Live streams are rejected.
type Media struct {
	Segments     []Segment
	Target       int
	MediaSeq     uint64
	MapURI       string
	MapByteStart int64
	MapByteEnd   int64 // inclusive; -1 if whole resource
	IsFMP4       bool
}

type Segment struct {
	URI       string
	Duration  float64
	KeyURI    string
	KeyIV     string
	MediaSeq  uint64
	ByteStart int64
	ByteEnd   int64 // inclusive, -1 if not a byte range
}

// Variant is one STREAM-INF entry from a master playlist.
type Variant struct {
	URI        string
	Bandwidth  uint32
	AverageBW  uint32
	Resolution string
	Codecs     string
	Name       string
	FrameRate  float64
}

// Playlist is either a master (variants) or a media playlist.
type Playlist struct {
	IsMaster bool
	Master   []Variant
	Media    *Media
}

// Parse reads a master or media playlist. Live and SAMPLE-AES / DRM are rejected.
func Parse(r io.Reader) (*Playlist, error) {
	p, listType, err := m3u8.DecodeFrom(r, true)
	if err != nil {
		return nil, err
	}
	switch listType {
	case m3u8.MASTER:
		mp := p.(*m3u8.MasterPlaylist)
		out := &Playlist{IsMaster: true}
		for _, v := range mp.Variants {
			if v == nil || v.URI == "" {
				continue
			}
			item := Variant{
				URI:       v.URI,
				Bandwidth: v.Bandwidth,
				AverageBW: v.AverageBandwidth,
				Codecs:    v.Codecs,
				Name:      v.Name,
				FrameRate: v.FrameRate,
			}
			item.Resolution = v.Resolution
			out.Master = append(out.Master, item)
		}
		if len(out.Master) == 0 {
			return nil, fmt.Errorf("主播放列表没有可用清晰度")
		}
		return out, nil
	case m3u8.MEDIA:
		media, err := mediaFrom(p.(*m3u8.MediaPlaylist))
		if err != nil {
			return nil, err
		}
		return &Playlist{Media: media}, nil
	default:
		return nil, fmt.Errorf("未知播放列表类型")
	}
}

// ParseMedia reads a media playlist and requires EXT-X-ENDLIST.
func ParseMedia(r io.Reader) (*Media, error) {
	pl, err := Parse(r)
	if err != nil {
		return nil, err
	}
	if pl.IsMaster || pl.Media == nil {
		return nil, fmt.Errorf("expected media playlist, got master")
	}
	return pl.Media, nil
}

func mediaFrom(pl *m3u8.MediaPlaylist) (*Media, error) {
	if !pl.Closed {
		return nil, fmt.Errorf("直播播放列表不受支持（缺少 EXT-X-ENDLIST）")
	}
	out := &Media{
		Target:       int(pl.TargetDuration),
		MediaSeq:     pl.SeqNo,
		MapByteStart: -1,
		MapByteEnd:   -1,
	}
	if pl.Map != nil && pl.Map.URI != "" {
		out.MapURI = pl.Map.URI
		out.IsFMP4 = true
		if pl.Map.Limit > 0 {
			out.MapByteStart = pl.Map.Offset
			out.MapByteEnd = pl.Map.Offset + pl.Map.Limit - 1
		}
	}
	seq := pl.SeqNo
	var curKeyURI, curKeyIV string
	if pl.Key != nil && pl.Key.Method != "" && pl.Key.Method != "NONE" {
		method := strings.ToUpper(pl.Key.Method)
		if method != "AES-128" {
			return nil, fmt.Errorf("不支持的加密：%s（拒绝 SAMPLE-AES / DRM）", pl.Key.Method)
		}
		if kf := strings.TrimSpace(pl.Key.Keyformat); kf != "" && !strings.EqualFold(kf, "identity") {
			return nil, fmt.Errorf("不支持的 KEYFORMAT：%s（仅 identity）", kf)
		}
		curKeyURI = pl.Key.URI
		curKeyIV = pl.Key.IV
	}
	for _, seg := range pl.Segments {
		if seg == nil {
			continue
		}
		if seg.Key != nil && seg.Key.Method != "" && seg.Key.Method != "NONE" {
			method := strings.ToUpper(seg.Key.Method)
			if method != "AES-128" {
				return nil, fmt.Errorf("不支持的加密：%s（拒绝 SAMPLE-AES / DRM）", seg.Key.Method)
			}
			if kf := strings.TrimSpace(seg.Key.Keyformat); kf != "" &&
				!strings.EqualFold(kf, "identity") {
				return nil, fmt.Errorf("不支持的 KEYFORMAT：%s（仅 identity）", kf)
			}
			curKeyURI = seg.Key.URI
			curKeyIV = seg.Key.IV
		}
		item := Segment{
			URI:       seg.URI,
			Duration:  seg.Duration,
			KeyURI:    curKeyURI,
			KeyIV:     curKeyIV,
			MediaSeq:  seq,
			ByteStart: -1,
			ByteEnd:   -1,
		}
		if strings.HasSuffix(strings.ToLower(seg.URI), ".m4s") ||
			strings.HasSuffix(strings.ToLower(seg.URI), ".mp4") {
			out.IsFMP4 = true
		}
		if seg.Limit > 0 {
			item.ByteEnd = seg.Offset + seg.Limit - 1
			item.ByteStart = seg.Offset
		}
		out.Segments = append(out.Segments, item)
		seq++
	}
	if len(out.Segments) == 0 {
		return nil, fmt.Errorf("播放列表没有分片")
	}
	return out, nil
}

// Label formats a human-readable quality name for a variant.
func (v Variant) Label() string {
	if v.Name != "" {
		return v.Name
	}
	if v.Resolution != "" {
		return v.Resolution
	}
	if v.Bandwidth > 0 {
		kbps := v.Bandwidth / 1000
		if kbps == 0 {
			kbps = 1
		}
		return fmt.Sprintf("%dk", kbps)
	}
	return "default"
}
