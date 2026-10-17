package engine

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	probeMaxAttempts = 3
	probeRetryCap    = 10 * time.Second
)

var contentDispositionRE = regexp.MustCompile(`(?i)filename\*?=(?:UTF-8''|")?([^";\r\n]+)`)

type ProbeResult struct {
	FinalURL     string
	Size         int64
	AcceptRanges bool
	ETag         string
	LastModified string
	Filename     string
	ContentType  string
	SingleStream bool // server ignored Range / returned 200
}

// Probe does GET with Range: bytes=0-0 and Accept-Encoding: identity.
// Network errors and 408/425/429/500/502/503/504 are retried up to 3 attempts.
func Probe(ctx context.Context, client *http.Client, rawURL string, headers http.Header) (*ProbeResult, error) {
	var lastErr error
	for attempt := 0; attempt < probeMaxAttempts; attempt++ {
		pr, retryAfter, err := probeOnce(ctx, client, rawURL, headers)
		if err == nil {
			return pr, nil
		}
		lastErr = err
		te, ok := err.(*TaskError)
		if !ok || !probeRetryable(te) {
			return nil, err
		}
		if attempt+1 >= probeMaxAttempts {
			break
		}
		wait := probeBackoff(attempt + 1)
		if retryAfter > 0 {
			if retryAfter > probeRetryCap {
				retryAfter = probeRetryCap
			}
			wait = retryAfter
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return nil, lastErr
		}
	}
	if lastErr == nil {
		lastErr = NewTaskError(ErrCodeProbeFailed, "探测失败", "稍后重试或改用浏览器下载")
	}
	return nil, lastErr
}

func probeOnce(ctx context.Context, client *http.Client, rawURL string, headers http.Header) (*ProbeResult, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	copyHeaders(req.Header, headers)
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept-Encoding", "identity")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUA)
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, NewTaskError(ErrCodeCanceled, ctx.Err().Error(), "")
		}
		return nil, 0, NewTaskError(ErrCodeProbeFailed, err.Error(), "检查网络或代理设置")
	}
	defer resp.Body.Close()
	io.CopyN(io.Discard, resp.Body, 64*1024)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, 0, NewTaskError(ErrCodeAuthRequired, "需要登录", "刷新浏览器 Cookie 后重试")
	case http.StatusForbidden:
		return nil, 0, NewTaskError(ErrCodeForbidden, "服务器拒绝访问", "用浏览器打开链接确认权限")
	case http.StatusNotFound:
		return nil, 0, NewTaskError(ErrCodeNotFound, "资源不存在", "确认链接是否过期")
	}

	if probeStatusRetryable(resp.StatusCode) {
		wait := time.Duration(0)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			wait = parseRetryAfter(resp.Header.Get("Retry-After"))
		}
		return nil, wait, NewTaskError(ErrCodeProbeFailed,
			fmt.Sprintf("HTTP %d", resp.StatusCode),
			"稍后重试或改用浏览器下载")
	}

	pr := &ProbeResult{
		FinalURL:     resp.Request.URL.String(),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		ContentType:  resp.Header.Get("Content-Type"),
		Filename:     filenameFromResponse(resp),
	}

	switch resp.StatusCode {
	case http.StatusPartialContent:
		pr.AcceptRanges = true
		pr.Size = parseContentRangeTotal(resp.Header.Get("Content-Range"))
		if pr.Size <= 0 {
			if cl := resp.Header.Get("Content-Length"); cl != "" {
				if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
					pr.Size = n // unlikely for 206 with 0-0, but keep
				}
			}
		}
	case http.StatusOK:
		pr.SingleStream = true
		pr.AcceptRanges = false
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
				pr.Size = n
			}
		}
		if strings.EqualFold(resp.Header.Get("Accept-Ranges"), "bytes") {
			// Some servers advertise Accept-Ranges but still return 200 for Range:0-0.
			// Treat as unknown; fetcher will retry with real ranges and may upgrade.
			pr.AcceptRanges = true
		}
	default:
		if resp.StatusCode >= 400 {
			return nil, 0, NewTaskError(ErrCodeProbeFailed,
				fmt.Sprintf("HTTP %d", resp.StatusCode),
				"稍后重试或改用浏览器下载")
		}
		pr.SingleStream = true
	}

	return pr, 0, nil
}

func probeStatusRetryable(code int) bool {
	switch code {
	case http.StatusRequestTimeout, // 408
		425,
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

func probeRetryable(te *TaskError) bool {
	if te == nil {
		return false
	}
	switch te.Code {
	case ErrCodeProbeFailed, ErrCodeNetwork:
		return true
	default:
		return false
	}
}

func probeBackoff(attempt int) time.Duration {
	// attempt is 1-based wait index after a failure.
	if attempt < 1 {
		attempt = 1
	}
	base := 200 * time.Millisecond
	d := base << (attempt - 1)
	if d > probeRetryCap {
		d = probeRetryCap
	}
	jitter := time.Duration(rand.Int63n(int64(d/2 + 1)))
	return d/2 + jitter
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if sec, err := strconv.Atoi(v); err == nil {
		if sec < 0 {
			return 0
		}
		d := time.Duration(sec) * time.Second
		if d > probeRetryCap {
			return probeRetryCap
		}
		return d
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		if d > probeRetryCap {
			return probeRetryCap
		}
		return d
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// parseContentRange parses "bytes START-END/TOTAL" or "bytes START-END/*".
// star is true when the total is unspecified.
func parseContentRange(cr string) (start, end, total int64, star, ok bool) {
	cr = strings.TrimSpace(cr)
	if cr == "" {
		return 0, 0, 0, false, false
	}
	lower := strings.ToLower(cr)
	if !strings.HasPrefix(lower, "bytes ") && !strings.HasPrefix(lower, "bytes=") {
		return 0, 0, 0, false, false
	}
	rest := cr
	if i := strings.IndexAny(cr, " ="); i >= 0 {
		rest = strings.TrimSpace(cr[i+1:])
	}
	slash := strings.LastIndex(rest, "/")
	if slash < 0 {
		return 0, 0, 0, false, false
	}
	span := rest[:slash]
	tot := rest[slash+1:]
	dash := strings.Index(span, "-")
	if dash < 0 {
		return 0, 0, 0, false, false
	}
	var err error
	start, err = strconv.ParseInt(strings.TrimSpace(span[:dash]), 10, 64)
	if err != nil {
		return 0, 0, 0, false, false
	}
	end, err = strconv.ParseInt(strings.TrimSpace(span[dash+1:]), 10, 64)
	if err != nil {
		return 0, 0, 0, false, false
	}
	if tot == "*" {
		return start, end, -1, true, true
	}
	total, err = strconv.ParseInt(strings.TrimSpace(tot), 10, 64)
	if err != nil {
		return 0, 0, 0, false, false
	}
	return start, end, total, false, true
}

func parseContentRangeTotal(cr string) int64 {
	// bytes 0-0/12345
	if cr == "" {
		return -1
	}
	i := strings.LastIndex(cr, "/")
	if i < 0 || i+1 >= len(cr) {
		return -1
	}
	total := cr[i+1:]
	if total == "*" {
		return -1
	}
	n, err := strconv.ParseInt(total, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func filenameFromResponse(resp *http.Response) string {
	cd := resp.Header.Get("Content-Disposition")
	if cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if fn := params["filename"]; fn != "" {
				return path.Base(fn)
			}
		}
		if m := contentDispositionRE.FindStringSubmatch(cd); len(m) > 1 {
			fn := strings.Trim(m[1], `"'`)
			if fn != "" {
				return path.Base(fn)
			}
		}
	}
	u := resp.Request.URL
	base := path.Base(u.Path)
	if base != "" && base != "/" && base != "." {
		return base
	}
	return fmt.Sprintf("download-%d", time.Now().Unix())
}

func copyHeaders(dst, src http.Header) {
	if src == nil {
		return
	}
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

const defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 XDL/0.1"
