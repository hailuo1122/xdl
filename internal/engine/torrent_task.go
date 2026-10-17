package engine

import (
	"context"
	"encoding/json"

	"github.com/xdl-app/xdl/internal/bittorrent"
)

// TorrentDownloader adapts bittorrent.Client into the task manager.
type TorrentDownloader struct {
	client *bittorrent.Client
}

func NewTorrentDownloader(client *bittorrent.Client) *TorrentDownloader {
	return &TorrentDownloader{client: client}
}

func (d *TorrentDownloader) Client() *bittorrent.Client {
	if d == nil {
		return nil
	}
	return d.client
}

type TorrentRunOptions struct {
	URL        string
	Dir        string
	Name       string
	KindMeta   json.RawMessage
	OnProgress func(done, total, speed int64)
	OnMeta     func(json.RawMessage) error
}

type TorrentRunResult struct {
	Path string
	Name string
	Size int64
}

func (d *TorrentDownloader) Download(ctx context.Context, opt TorrentRunOptions) (*TorrentRunResult, error) {
	if d == nil || d.client == nil {
		return nil, NewTaskError(ErrCodeInternal, "BT 客户端未启动", "重启应用后重试")
	}
	meta := bittorrent.DecodeMeta(opt.KindMeta)
	magnet := meta.Magnet
	torrentPath := meta.TorrentPath
	if magnet == "" && bittorrent.IsMagnet(opt.URL) {
		magnet = opt.URL
	}
	if torrentPath == "" && bittorrent.IsTorrentPath(opt.URL) {
		torrentPath = opt.URL
	}
	name := opt.Name
	if name == "" {
		name = meta.Name
	}
	res, err := d.client.Download(ctx, bittorrent.Options{
		Magnet:      magnet,
		TorrentPath: torrentPath,
		InfoHash:    meta.InfoHash,
		Dir:         opt.Dir,
		Name:        name,
		Selected:    meta.Selected,
		SeedAfter:   meta.SeedAfter,
		OnProgress:  opt.OnProgress,
		OnMeta: func(m bittorrent.Meta) {
			if opt.OnMeta == nil {
				return
			}
			raw, err := bittorrent.EncodeMeta(m)
			if err != nil {
				return
			}
			_ = opt.OnMeta(raw)
		},
	})
	if err != nil {
		return nil, err
	}
	return &TorrentRunResult{Path: res.Path, Name: res.Name, Size: res.Size}, nil
}
