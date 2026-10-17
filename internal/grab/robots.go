package grab

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
	"github.com/xdl-app/xdl/internal/netx"
)

const robotsUserAgent = "XDL"

// RobotsCache fetches and caches robots.txt per origin.
type RobotsCache struct {
	client *http.Client
	mu     sync.Mutex
	data   map[string]*robotstxt.RobotsData // key = origin
}

func NewRobotsCache(client *http.Client) *RobotsCache {
	if client == nil {
		client = netx.NewProbeClient(15 * time.Second)
	}
	return &RobotsCache{
		client: client,
		data:   make(map[string]*robotstxt.RobotsData),
	}
}

// Allowed reports whether ua may fetch targetURL per robots.txt.
// Fail-open on network/parse errors so a broken robots.txt does not block grabs.
func (c *RobotsCache) Allowed(ctx context.Context, targetURL string) bool {
	u, err := url.Parse(targetURL)
	if err != nil || u.Host == "" {
		return false
	}
	origin := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
	c.mu.Lock()
	rd, ok := c.data[origin]
	c.mu.Unlock()
	if !ok {
		rd = c.fetch(ctx, origin)
		c.mu.Lock()
		c.data[origin] = rd
		c.mu.Unlock()
	}
	if rd == nil {
		return true
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path = path + "?" + u.RawQuery
	}
	return rd.TestAgent(path, robotsUserAgent)
}

func (c *RobotsCache) fetch(ctx context.Context, origin string) *robotstxt.RobotsData {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", robotsUserAgent+"/1.0")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden {
		// Empty allow-all.
		rd, _ := robotstxt.FromStatusAndBytes(resp.StatusCode, nil)
		return rd
	}
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil
	}
	rd, err := robotstxt.FromBytes(body)
	if err != nil {
		return nil
	}
	return rd
}

// robotsOrigin builds scheme://host for caching.
func robotsOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid url")
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host), nil
}
