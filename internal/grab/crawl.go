package grab

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xdl-app/xdl/internal/netx"
)

const (
	DefaultMaxDepth = 2
	DefaultMaxPages = 200
	DefaultHostConc = 2
	minDelay = 300 * time.Millisecond
	maxDelay = 1000 * time.Millisecond
)

// Options configure a site crawl / page probe.
type Options struct {
	StartURL   string
	MaxDepth   int // default 2; 0 = only start page
	MaxPages   int // default 200
	SameOrigin bool
	Filter     Filter
	Headers    http.Header
	// RespectRobots defaults true.
	RespectRobots *bool
	OnPage        func(pageURL string, depth int)
}

// Result is the aggregated candidate list from a crawl.
type Result struct {
	StartURL   string      `json:"startUrl"`
	Pages      int         `json:"pages"`
	Candidates []Candidate `json:"candidates"`
	Skipped    int         `json:"skipped"` // robots / filter rejects
	Errors     []string    `json:"errors,omitempty"`
}

type frontierItem struct {
	URL   string
	Depth int
}

// Crawl walks HTML pages from StartURL and collects downloadable candidates.
func Crawl(ctx context.Context, opt Options) (*Result, error) {
	start := NormalizeURL(opt.StartURL)
	if start == "" {
		return nil, fmt.Errorf("无效的起始 URL（仅支持 http/https）")
	}
	if opt.MaxDepth < 0 {
		opt.MaxDepth = 0
	}
	if opt.MaxDepth == 0 && opt.MaxPages == 0 {
		// Probe single page: depth 0, 1 page.
		opt.MaxDepth = 0
		opt.MaxPages = 1
	}
	if opt.MaxDepth <= 0 && opt.MaxPages > 1 {
		// Keep depth 0 = start only unless caller set depth.
	}
	if opt.MaxDepth > DefaultMaxDepth {
		opt.MaxDepth = DefaultMaxDepth
	}
	if opt.MaxPages <= 0 {
		opt.MaxPages = DefaultMaxPages
	}
	if opt.MaxPages > DefaultMaxPages {
		opt.MaxPages = DefaultMaxPages
	}
	respect := true
	if opt.RespectRobots != nil {
		respect = *opt.RespectRobots
	}
	if len(opt.Filter.Extensions) == 0 && opt.Filter.Regex == "" {
		opt.Filter.Extensions = DefaultBinaryExts()
	}

	client := netx.NewProbeClient(30 * time.Second)
	robots := NewRobotsCache(client)

	res := &Result{StartURL: start, Candidates: nil}
	seenPages := map[string]struct{}{start: {}}
	seenCand := map[string]struct{}{}
	queue := []frontierItem{{URL: start, Depth: 0}}

	var hostMu sync.Mutex
	lastFetch := map[string]time.Time{}

	for len(queue) > 0 && res.Pages < opt.MaxPages {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		item := queue[0]
		queue = queue[1:]

		if respect && !robots.Allowed(ctx, item.URL) {
			res.Skipped++
			continue
		}

		if err := throttle(ctx, &hostMu, lastFetch, item.URL); err != nil {
			return res, err
		}

		body, finalURL, ctype, err := fetchHTML(ctx, client, item.URL, opt.Headers)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", item.URL, err))
			if len(res.Errors) > 20 {
				res.Errors = res.Errors[:20]
			}
			continue
		}
		if !isHTMLContent(ctype, finalURL) {
			// Non-HTML leaf: treat as candidate if filter matches.
			c := Candidate{URL: NormalizeURL(finalURL), Source: "a", Ext: PathExt(finalURL), Page: item.URL}
			if c.URL == "" {
				c.URL = NormalizeURL(item.URL)
			}
			if opt.Filter.Match(c) {
				if _, ok := seenCand[c.URL]; !ok {
					seenCand[c.URL] = struct{}{}
					res.Candidates = append(res.Candidates, c)
				}
			} else {
				res.Skipped++
			}
			continue
		}

		res.Pages++
		if opt.OnPage != nil {
			opt.OnPage(finalURL, item.Depth)
		}

		cands, err := ExtractHTML(strings.NewReader(body), finalURL)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: parse: %v", finalURL, err))
			continue
		}
		for _, c := range cands {
			if !opt.Filter.Match(c) {
				res.Skipped++
				continue
			}
			if _, ok := seenCand[c.URL]; ok {
				continue
			}
			seenCand[c.URL] = struct{}{}
			c.Page = finalURL
			res.Candidates = append(res.Candidates, c)
		}

		if item.Depth >= opt.MaxDepth {
			continue
		}
		links, _ := ExtractPageLinks(strings.NewReader(body), finalURL)
		for _, link := range links {
			if opt.SameOrigin && !SameOrigin(start, link) {
				continue
			}
			if _, ok := seenPages[link]; ok {
				continue
			}
			// Only enqueue likely HTML pages for further crawl.
			ext := PathExt(link)
			if ext != "" && !isHTMLExt(ext) {
				continue
			}
			seenPages[link] = struct{}{}
			queue = append(queue, frontierItem{URL: link, Depth: item.Depth + 1})
		}
	}
	return res, nil
}

// ProbePage fetches a single page (depth 0) and returns filtered candidates.
func ProbePage(ctx context.Context, pageURL string, f Filter, headers http.Header) (*Result, error) {
	return Crawl(ctx, Options{
		StartURL:   pageURL,
		MaxDepth:   0,
		MaxPages:   1,
		SameOrigin: true,
		Filter:     f,
		Headers:    headers,
	})
}

func throttle(ctx context.Context, mu *sync.Mutex, last map[string]time.Time, pageURL string) error {
	origin, err := robotsOrigin(pageURL)
	if err != nil {
		return nil
	}
	mu.Lock()
	prev := last[origin]
	delay := minDelay + time.Duration(rand.Int63n(int64(maxDelay-minDelay)))
	wait := time.Until(prev.Add(delay))
	mu.Unlock()
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	mu.Lock()
	last[origin] = time.Now()
	mu.Unlock()
	return nil
}

func fetchHTML(ctx context.Context, client *http.Client, pageURL string, headers http.Header) (body, final, ctype string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", robotsUserAgent+"/1.0 (site grabber; +https://localhost)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	final = pageURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	ctype = resp.Header.Get("Content-Type")
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", "", err
	}
	return string(raw), final, ctype, nil
}

func isHTMLContent(ctype, finalURL string) bool {
	ct := strings.ToLower(ctype)
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml") {
		return true
	}
	if ct == "" || strings.Contains(ct, "text/plain") {
		ext := PathExt(finalURL)
		return ext == "" || isHTMLExt(ext)
	}
	return false
}
