package bittorrent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"
)

// Config controls the process-wide BitTorrent client.
type Config struct {
	// DataDir is the root for piece-completion DB and default storage.
	DataDir string
	// ListenPort 0 lets the OS pick an ephemeral port.
	ListenPort int
	// MaxPeers caps established peer connections per torrent (default 50).
	MaxPeers int
	// UploadLimitBps limits upload; 0 = unlimited (still gated by Seed/NoUpload).
	UploadLimitBps int64
	// DownloadLimitBps limits BT download; 0 = unlimited.
	DownloadLimitBps int64
	// Seed enables altruistic seeding after completion when a task opts in.
	Seed bool
}

// Client wraps a single anacrolix torrent.Client for the whole app.
type Client struct {
	mu     sync.Mutex
	cfg    Config
	cl     *torrent.Client
	pc     storage.PieceCompletion
	upload *rate.Limiter
	down   *rate.Limiter
}

// NewClient creates (but does not dial peers until torrents are added).
func NewClient(cfg Config) (*Client, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("bittorrent: DataDir required")
	}
	if cfg.MaxPeers <= 0 {
		cfg.MaxPeers = 50
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	completionDir := filepath.Join(cfg.DataDir, "completion")
	if err := os.MkdirAll(completionDir, 0o755); err != nil {
		return nil, err
	}
	pc, err := storage.NewDefaultPieceCompletionForDir(completionDir)
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, pc: pc}
	c.upload = limitOrNil(cfg.UploadLimitBps)
	c.down = limitOrNil(cfg.DownloadLimitBps)

	tc := torrent.NewDefaultClientConfig()
	tc.DataDir = filepath.Join(cfg.DataDir, "data")
	_ = os.MkdirAll(tc.DataDir, 0o755)
	tc.DefaultStorage = storage.NewFileWithCompletion(tc.DataDir, pc)
	tc.ListenPort = cfg.ListenPort
	tc.NoDefaultPortForwarding = true
	tc.Seed = cfg.Seed
	// Default conservative: no upload until a task explicitly seeds.
	tc.NoUpload = !cfg.Seed
	tc.UploadRateLimiter = c.upload
	tc.DownloadRateLimiter = c.down
	tc.DisableIPv6 = false

	cl, err := torrent.NewClient(tc)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	c.cl = cl
	return c, nil
}

func limitOrNil(bps int64) *rate.Limiter {
	if bps <= 0 {
		return rate.NewLimiter(rate.Inf, 0)
	}
	burst := int(bps)
	if burst < 16*1024 {
		burst = 16 * 1024
	}
	return rate.NewLimiter(rate.Limit(bps), burst)
}

// Apply updates runtime limits. Port changes require restart (ignored here).
func (c *Client) Apply(cfg Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cfg.MaxPeers > 0 {
		c.cfg.MaxPeers = cfg.MaxPeers
	}
	c.cfg.Seed = cfg.Seed
	c.cfg.UploadLimitBps = cfg.UploadLimitBps
	c.cfg.DownloadLimitBps = cfg.DownloadLimitBps
	if c.upload != nil {
		if cfg.UploadLimitBps <= 0 {
			c.upload.SetLimit(rate.Inf)
		} else {
			c.upload.SetLimit(rate.Limit(cfg.UploadLimitBps))
			burst := int(cfg.UploadLimitBps)
			if burst < 16*1024 {
				burst = 16 * 1024
			}
			c.upload.SetBurst(burst)
		}
	}
	if c.down != nil {
		if cfg.DownloadLimitBps <= 0 {
			c.down.SetLimit(rate.Inf)
		} else {
			c.down.SetLimit(rate.Limit(cfg.DownloadLimitBps))
			burst := int(cfg.DownloadLimitBps)
			if burst < 16*1024 {
				burst = 16 * 1024
			}
			c.down.SetBurst(burst)
		}
	}
}

// Close shuts down the client and piece completion store.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var first error
	if c.cl != nil {
		for _, err := range c.cl.Close() {
			if first == nil {
				first = err
			}
		}
		c.cl = nil
	}
	if c.pc != nil {
		if err := c.pc.Close(); err != nil && first == nil {
			first = err
		}
		c.pc = nil
	}
	return first
}

func (c *Client) raw() *torrent.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cl
}

func (c *Client) maxPeers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.MaxPeers
}

// Drop removes a torrent from the client by infohash hex.
func (c *Client) Drop(infoHash string) {
	cl := c.raw()
	if cl == nil || infoHash == "" {
		return
	}
	var ih torrent.InfoHash
	if err := ih.FromHexString(infoHash); err != nil {
		return
	}
	if t, ok := cl.Torrent(ih); ok {
		t.Drop()
	}
}
