package job

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xdl-app/xdl/internal/media/dash"
	"github.com/xdl-app/xdl/internal/media/hls"
	"github.com/xdl-app/xdl/internal/netx"
)

// Variant is a selectable quality option returned by Probe.
type Variant struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	URI       string `json:"uri"`
	Bandwidth int    `json:"bandwidth,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Codecs    string `json:"codecs,omitempty"`
}

// ProbeResult describes a media playlist for the UI / media.start.
type ProbeResult struct {
	Kind     string    `json:"kind"` // hls | dash
	URL      string    `json:"url"`
	Title    string    `json:"title,omitempty"`
	Variants []Variant `json:"variants"`
}

// DetectKind guesses media kind from URL path.
func DetectKind(rawURL string) string {
	u := strings.ToLower(strings.SplitN(rawURL, "?", 2)[0])
	switch {
	case strings.HasSuffix(u, ".m3u8"):
		return "hls"
	case strings.HasSuffix(u, ".mpd"):
		return "dash"
	default:
		return ""
	}
}

// Probe fetches and parses an HLS/DASH manifest, returning selectable variants.
func Probe(ctx context.Context, rawURL string, headers http.Header, proxy string) (*ProbeResult, error) {
	client := netx.NewProbeClient(30 * time.Second)
	if proxy != "" {
		var err error
		client, err = netx.WithProxy(client, proxy)
		if err != nil {
			return nil, err
		}
	}
	body, finalURL, err := fetchText(ctx, client, rawURL, headers)
	if err != nil {
		return nil, err
	}
	kind := DetectKind(finalURL)
	if kind == "" {
		kind = DetectKind(rawURL)
	}
	trim := strings.TrimSpace(body)
	if kind == "" {
		switch {
		case strings.HasPrefix(trim, "#EXTM3U"):
			kind = "hls"
		case strings.Contains(trim, "<MPD") || strings.Contains(trim, "<mpd"):
			kind = "dash"
		default:
			return nil, fmt.Errorf("无法识别为 HLS / DASH 播放列表")
		}
	}
	switch kind {
	case "hls":
		return probeHLS(finalURL, strings.NewReader(body))
	case "dash":
		return probeDASH(finalURL, strings.NewReader(body))
	default:
		return nil, fmt.Errorf("不支持的媒体类型")
	}
}

func probeHLS(finalURL string, r io.Reader) (*ProbeResult, error) {
	pl, err := hls.Parse(r)
	if err != nil {
		return nil, err
	}
	out := &ProbeResult{Kind: "hls", URL: finalURL, Title: hls.SuggestName(finalURL, "", "")}
	out.Title = strings.TrimSuffix(out.Title, ".ts")
	if pl.IsMaster {
		for i, v := range pl.Master {
			uri, err := hls.ResolveURL(finalURL, v.URI)
			if err != nil {
				return nil, err
			}
			w, h := parseRes(v.Resolution)
			out.Variants = append(out.Variants, Variant{
				ID:        fmt.Sprintf("%d", i),
				Label:     v.Label(),
				URI:       uri,
				Bandwidth: int(v.Bandwidth),
				Width:     w,
				Height:    h,
				Codecs:    v.Codecs,
			})
		}
		sort.SliceStable(out.Variants, func(i, j int) bool {
			return out.Variants[i].Bandwidth > out.Variants[j].Bandwidth
		})
		return out, nil
	}
	out.Variants = []Variant{{
		ID:    "0",
		Label: "default",
		URI:   finalURL,
	}}
	return out, nil
}

func probeDASH(finalURL string, r io.Reader) (*ProbeResult, error) {
	man, err := dash.Parse(r, finalURL)
	if err != nil {
		return nil, err
	}
	out := &ProbeResult{Kind: "dash", URL: finalURL, Title: strings.TrimSuffix(dash.SuggestName(finalURL, ""), ".mp4")}
	for i, v := range man.Variants {
		id := v.ID
		if id == "" {
			id = fmt.Sprintf("%d", i)
		}
		out.Variants = append(out.Variants, Variant{
			ID:        id,
			Label:     v.Label,
			URI:       finalURL,
			Bandwidth: v.Bandwidth,
			Width:     v.Width,
			Height:    v.Height,
			Codecs:    v.Codecs,
		})
	}
	sort.SliceStable(out.Variants, func(i, j int) bool {
		return out.Variants[i].Bandwidth > out.Variants[j].Bandwidth
	})
	return out, nil
}

func parseRes(s string) (int, int) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, 0
	}
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	return w, h
}

func fetchText(ctx context.Context, client *http.Client, rawURL string, headers http.Header) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "XDL/0.0.3")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("播放列表 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", "", err
	}
	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return string(b), final, nil
}
