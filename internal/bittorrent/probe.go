package bittorrent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

const defaultMetaTimeout = 90 * time.Second

// Probe resolves magnet or .torrent metadata and returns a file list.
// After success the torrent is dropped from the client; metainfo is cached
// under DataDir/meta so Start can reopen with the correct download directory.
func (c *Client) Probe(ctx context.Context, source string) (*ProbeResult, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("空的磁力链接或种子路径")
	}
	// Local .torrent: parse offline — no peer traffic.
	if IsTorrentPath(source) {
		return probeTorrentFile(source)
	}
	if !IsMagnet(source) {
		return nil, fmt.Errorf("只接受 magnet: 链接或 .torrent 文件路径")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultMetaTimeout)
		defer cancel()
	}

	cl := c.raw()
	if cl == nil {
		return nil, fmt.Errorf("BT 客户端未启动")
	}
	spec, err := torrent.TorrentSpecFromMagnetUri(source)
	if err != nil {
		return nil, fmt.Errorf("无效的磁力链接：%w", err)
	}
	spec.DisallowDataDownload = true
	spec.DisallowDataUpload = true
	t, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		return nil, err
	}
	defer t.Drop()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("获取种子元数据超时，请检查磁力链接或网络后重试")
	case <-t.GotInfo():
	}

	out, err := probeFromTorrent(t, "magnet", source)
	if err != nil {
		return nil, err
	}
	if err := c.cacheMeta(t); err != nil {
		return nil, err
	}
	return out, nil
}

func probeTorrentFile(path string) (*ProbeResult, error) {
	mi, err := metainfo.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("无法读取种子文件：%w", err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, fmt.Errorf("种子文件损坏：%w", err)
	}
	files := make([]FileInfo, 0)
	var total int64
	for i, f := range info.UpvertedFiles() {
		p := strings.Join(f.BestPath(), "/")
		if p == "" {
			p = info.BestName()
		}
		files = append(files, FileInfo{Index: i, Path: p, Length: f.Length})
		total += f.Length
	}
	ih := mi.HashInfoBytes()
	return &ProbeResult{
		InfoHash:  ih.HexString(),
		Name:      info.BestName(),
		TotalSize: total,
		Files:     files,
		Source:    "file",
	}, nil
}

func probeFromTorrent(t *torrent.Torrent, kind, source string) (*ProbeResult, error) {
	info := t.Info()
	if info == nil {
		return nil, fmt.Errorf("种子元数据无效")
	}
	files := make([]FileInfo, 0, len(t.Files()))
	var total int64
	for i, f := range t.Files() {
		files = append(files, FileInfo{
			Index:  i,
			Path:   f.DisplayPath(),
			Length: f.Length(),
		})
		total += f.Length()
	}
	name := t.Name()
	if name == "" {
		name = info.BestName()
	}
	out := &ProbeResult{
		InfoHash:  t.InfoHash().HexString(),
		Name:      name,
		TotalSize: total,
		Files:     files,
		Source:    kind,
	}
	if kind == "magnet" {
		out.Magnet = source
	}
	return out, nil
}

func (c *Client) cacheMeta(t *torrent.Torrent) error {
	dir := filepath.Join(c.cfg.DataDir, "meta")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, t.InfoHash().HexString()+".torrent")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	mi := t.Metainfo()
	return mi.Write(f)
}

func (c *Client) cachedMetaPath(infoHash string) string {
	if infoHash == "" {
		return ""
	}
	return filepath.Join(c.cfg.DataDir, "meta", infoHash+".torrent")
}
