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
	"github.com/anacrolix/torrent/storage"
)

// Options drives a single torrent download run.
type Options struct {
	Magnet      string
	TorrentPath string
	InfoHash    string
	Dir         string
	Name        string
	Selected    []int
	SeedAfter   bool
	OnProgress  func(done, total, speed int64)
	OnMeta      func(Meta)
}

// Result is returned when selected files finish downloading.
type Result struct {
	Path string
	Name string
	Size int64
	Meta Meta
}

// Download fetches the selected files. Canceling ctx pauses (download disallowed)
// without dropping the torrent from the client.
func (c *Client) Download(ctx context.Context, opt Options) (*Result, error) {
	if opt.Dir == "" {
		return nil, fmt.Errorf("下载目录不能为空")
	}
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, err
	}

	t, err := c.ensureTorrent(opt)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		t.DisallowDataDownload()
		return nil, ctx.Err()
	case <-t.GotInfo():
	}
	if t.Info() == nil {
		return nil, fmt.Errorf("种子元数据无效")
	}

	name := opt.Name
	if name == "" {
		name = t.Name()
	}
	name = sanitizeName(name)

	applySelection(t, opt.Selected)
	if n := c.maxPeers(); n > 0 {
		t.SetMaxEstablishedConns(n)
	}
	t.AllowDataDownload()
	if opt.SeedAfter {
		t.AllowDataUpload()
	} else {
		t.DisallowDataUpload()
	}

	total := selectedLength(t, opt.Selected)
	meta := Meta{
		InfoHash:    t.InfoHash().HexString(),
		Magnet:      opt.Magnet,
		TorrentPath: opt.TorrentPath,
		Name:        name,
		Selected:    append([]int(nil), opt.Selected...),
		SeedAfter:   opt.SeedAfter,
		DataDir:     opt.Dir,
	}
	if opt.OnMeta != nil {
		opt.OnMeta(meta)
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastDone int64
	lastAt := time.Now()

	for {
		done := selectedCompleted(t, opt.Selected)
		now := time.Now()
		elapsed := now.Sub(lastAt).Seconds()
		var speed int64
		if elapsed > 0 {
			speed = int64(float64(done-lastDone) / elapsed)
			if speed < 0 {
				speed = 0
			}
		}
		lastDone = done
		lastAt = now

		st := t.Stats()
		meta.Peers = st.ActivePeers
		meta.Seeds = st.ConnectedSeeders
		meta.Downloaded = st.BytesReadUsefulData.Int64()
		meta.Uploaded = st.BytesWrittenData.Int64()
		if meta.Downloaded > 0 {
			meta.Ratio = float64(meta.Uploaded) / float64(meta.Downloaded)
		}
		if opt.OnProgress != nil {
			opt.OnProgress(done, total, speed)
		}
		if opt.OnMeta != nil {
			opt.OnMeta(meta)
		}

		if total > 0 && done >= total {
			if !opt.SeedAfter {
				t.DisallowDataDownload()
				t.DisallowDataUpload()
			}
			path := outputPath(opt.Dir, name, t, opt.Selected)
			return &Result{Path: path, Name: name, Size: total, Meta: meta}, nil
		}

		select {
		case <-ctx.Done():
			t.DisallowDataDownload()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) ensureTorrent(opt Options) (*torrent.Torrent, error) {
	cl := c.raw()
	if cl == nil {
		return nil, fmt.Errorf("BT 客户端未启动")
	}
	if opt.InfoHash != "" {
		var ih torrent.InfoHash
		if err := ih.FromHexString(opt.InfoHash); err == nil {
			if t, ok := cl.Torrent(ih); ok {
				return t, nil
			}
		}
	}

	c.mu.Lock()
	pc := c.pc
	c.mu.Unlock()
	stor := storage.NewFileWithCompletion(opt.Dir, pc)

	tryAddMeta := func(path string) (*torrent.Torrent, error) {
		mi, err := metainfo.LoadFromFile(path)
		if err != nil {
			return nil, err
		}
		spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
		if err != nil {
			return nil, err
		}
		spec.Storage = stor
		spec.DisallowDataDownload = true
		spec.DisallowDataUpload = true
		t, _, err := cl.AddTorrentSpec(spec)
		return t, err
	}

	if opt.InfoHash != "" {
		if cached := c.cachedMetaPath(opt.InfoHash); cached != "" {
			if _, err := os.Stat(cached); err == nil {
				if t, err := tryAddMeta(cached); err == nil {
					return t, nil
				}
			}
		}
	}
	if opt.TorrentPath != "" {
		if t, err := tryAddMeta(opt.TorrentPath); err == nil {
			_ = c.cacheMeta(t)
			return t, nil
		} else {
			return nil, fmt.Errorf("无法读取种子文件：%w", err)
		}
	}
	if opt.Magnet != "" {
		spec, err := torrent.TorrentSpecFromMagnetUri(opt.Magnet)
		if err != nil {
			return nil, fmt.Errorf("无效的磁力链接：%w", err)
		}
		spec.Storage = stor
		spec.DisallowDataDownload = true
		spec.DisallowDataUpload = true
		t, _, err := cl.AddTorrentSpec(spec)
		if err != nil {
			return nil, err
		}
		return t, nil
	}
	return nil, fmt.Errorf("缺少磁力链接或种子文件")
}

func applySelection(t *torrent.Torrent, selected []int) {
	files := t.Files()
	if len(selected) == 0 {
		for _, f := range files {
			f.SetPriority(torrent.PiecePriorityNormal)
		}
		t.DownloadAll()
		return
	}
	want := make(map[int]struct{}, len(selected))
	for _, i := range selected {
		want[i] = struct{}{}
	}
	for i, f := range files {
		if _, ok := want[i]; ok {
			f.SetPriority(torrent.PiecePriorityNormal)
			f.Download()
		} else {
			f.SetPriority(torrent.PiecePriorityNone)
			f.Cancel()
		}
	}
}

func selectedLength(t *torrent.Torrent, selected []int) int64 {
	files := t.Files()
	if len(selected) == 0 {
		return t.Length()
	}
	var n int64
	for _, i := range selected {
		if i >= 0 && i < len(files) {
			n += files[i].Length()
		}
	}
	return n
}

func selectedCompleted(t *torrent.Torrent, selected []int) int64 {
	files := t.Files()
	if len(selected) == 0 {
		return t.BytesCompleted()
	}
	var n int64
	for _, i := range selected {
		if i >= 0 && i < len(files) {
			n += files[i].BytesCompleted()
		}
	}
	return n
}

func outputPath(dir, name string, t *torrent.Torrent, selected []int) string {
	files := t.Files()
	if len(selected) == 1 && selected[0] >= 0 && selected[0] < len(files) {
		return filepath.Join(dir, files[selected[0]].Path())
	}
	if len(files) == 1 {
		return filepath.Join(dir, files[0].Path())
	}
	return filepath.Join(dir, name)
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "torrent"
	}
	repl := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	name = repl.Replace(name)
	name = strings.Trim(name, " .")
	if name == "" {
		return "torrent"
	}
	return name
}
