package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/xdl-app/xdl/internal/grab"
	"github.com/xdl-app/xdl/internal/media/job"
	"github.com/xdl-app/xdl/internal/settings"
	"github.com/xdl-app/xdl/internal/updater"
)

// GrabRequest drives page probe / site crawl from the shell.
type GrabRequest struct {
	URL        string   `json:"url"`
	MaxDepth   int      `json:"maxDepth,omitempty"`
	MaxPages   int      `json:"maxPages,omitempty"`
	SameOrigin *bool    `json:"sameOrigin,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
	Regex      string   `json:"regex,omitempty"`
	Referer    string   `json:"referer,omitempty"`
	UserAgent  string   `json:"userAgent,omitempty"`
	Cookie     string   `json:"cookie,omitempty"`
}

// GrabEnqueueRequest turns selected candidates into download tasks.
type GrabEnqueueRequest struct {
	URLs  []string `json:"urls"`
	Dir   string   `json:"dir,omitempty"`
	Conns int      `json:"conns,omitempty"`
}

// BatchAddRequest imports many URLs at once (deduped).
type BatchAddRequest struct {
	Text  string   `json:"text,omitempty"`
	URLs  []string `json:"urls,omitempty"`
	Dir   string   `json:"dir,omitempty"`
	Conns int      `json:"conns,omitempty"`
}

// BatchAddResult summarizes a batch import.
type BatchAddResult struct {
	Added    []TaskView `json:"added"`
	Skipped  int        `json:"skipped"`
	Failures []string   `json:"failures,omitempty"`
}

// GrabProbe crawls/extracts candidates for the preview UI.
func (rt *Runtime) GrabProbe(req GrabRequest) (*grab.Result, error) {
	u := strings.TrimSpace(req.URL)
	if !safeURL(u) || strings.HasPrefix(strings.ToLower(u), "magnet:") {
		return nil, fmt.Errorf("只接受 http(s) 页面地址")
	}
	same := true
	if req.SameOrigin != nil {
		same = *req.SameOrigin
	}
	depth := req.MaxDepth
	if depth < 0 {
		depth = 0
	}
	pages := req.MaxPages
	if pages <= 0 {
		if depth == 0 {
			pages = 1
		} else {
			pages = grab.DefaultMaxPages
		}
	}
	f := grab.Filter{Extensions: req.Extensions, Regex: strings.TrimSpace(req.Regex)}
	h := http.Header{}
	if req.Referer != "" {
		h.Set("Referer", req.Referer)
	}
	if req.UserAgent != "" {
		h.Set("User-Agent", req.UserAgent)
	}
	if req.Cookie != "" {
		h.Set("Cookie", req.Cookie)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return grab.Crawl(ctx, grab.Options{
		StartURL:   u,
		MaxDepth:   depth,
		MaxPages:   pages,
		SameOrigin: same,
		Filter:     f,
		Headers:    h,
	})
}

// GrabEnqueue creates HTTP/media tasks for selected URLs.
func (rt *Runtime) GrabEnqueue(req GrabEnqueueRequest) (*BatchAddResult, error) {
	urls := grab.Dedup(req.URLs)
	if len(urls) == 0 {
		return nil, fmt.Errorf("没有可下载的链接")
	}
	return rt.addMany(urls, req.Dir, req.Conns), nil
}

// BatchAdd parses a pasted/imported URL list and enqueues tasks.
func (rt *Runtime) BatchAdd(req BatchAddRequest) (*BatchAddResult, error) {
	var urls []string
	if len(req.URLs) > 0 {
		urls = grab.Dedup(req.URLs)
	} else {
		urls = grab.ParseURLList(req.Text)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("未找到有效的 http(s) 链接")
	}
	return rt.addMany(urls, req.Dir, req.Conns), nil
}

func (rt *Runtime) addMany(urls []string, dir string, conns int) *BatchAddResult {
	out := &BatchAddResult{}
	for _, u := range urls {
		req := AddRequest{URL: u, Dir: dir, Conns: conns}
		var (
			v   *TaskView
			err error
		)
		switch mediaKindHint(u) {
		case "hls", "dash":
			req.Kind = mediaKindHint(u)
			v, err = rt.MediaStart(req, true)
		default:
			v, err = rt.Add(req, true)
		}
		if err != nil {
			out.Failures = append(out.Failures, u+": "+err.Error())
			out.Skipped++
			continue
		}
		out.Added = append(out.Added, *v)
	}
	return out
}

// applyCategory fills Dir/Category from settings when the caller did not pick a dir.
func (rt *Runtime) applyCategory(rawURL, name, dir string) (outDir, category string) {
	s := rt.Settings()
	if strings.TrimSpace(dir) != "" {
		return dir, ""
	}
	base := s.DownloadDir
	if name == "" {
		name = suggestNameFromURL(rawURL)
	}
	outDir, category = settings.ResolveDir(base, s.Categories, s.AutoCategory, rawURL, name)
	if outDir != base {
		_ = os.MkdirAll(outDir, 0o755)
	}
	return outDir, category
}

// CheckUpdate fetches the configured update manifesto and compares versions.
func (rt *Runtime) CheckUpdate() updater.CheckResult {
	s := rt.Settings()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	return updater.Check(ctx, Version, s.UpdateManifestURL, nil)
}

// suggestNameFromURL is a leaf filename hint for category matching.
func suggestNameFromURL(u string) string {
	u = strings.Split(u, "?")[0]
	u = strings.ReplaceAll(u, "\\", "/")
	return path.Base(u)
}

// mediaKindHint returns hls/dash kind string for DetectKind results.
func mediaKindHint(u string) string {
	return job.DetectKind(u)
}
