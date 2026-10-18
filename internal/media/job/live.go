package job

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xdl-app/xdl/internal/media/hls"
	"github.com/xdl-app/xdl/internal/media/remux"
)

// downloadHLSLive polls a live MPEG-TS playlist and appends segments until the
// stream ends (EXT-X-ENDLIST) or the task is paused/canceled.
func (d *Downloader) downloadHLSLive(ctx context.Context, opt Options, meta Meta, mediaURL string, first *hls.Media) (*Result, error) {
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, err
	}
	name := opt.Name
	if name == "" {
		name = hls.SuggestName(opt.URL, meta.Label, ".ts")
	}
	name = sanitizeLeaf(name)
	if filepath.Ext(name) == "" {
		name += ".ts"
	} else if filepath.Ext(name) == ".mp4" {
		name = name[:len(name)-4] + ".ts"
	}

	finalPath := opt.FinalPath
	if finalPath == "" {
		var err error
		finalPath, err = uniquePath(opt.Dir, name)
		if err != nil {
			return nil, err
		}
	} else if filepath.Ext(finalPath) == ".mp4" {
		finalPath = finalPath[:len(finalPath)-4] + ".ts"
	}

	part := finalPath + ".xdlpart"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	meta.WorkDir = ""
	meta.Live = true
	meta.LiveMode = "record"
	meta.Container = "ts"
	if opt.OnMeta != nil {
		_ = opt.OnMeta(meta)
	}

	keyCache := &sync.Map{}
	seen := map[uint64]struct{}{}
	var doneBytes int64
	lastAt := time.Now()
	lastBytes := int64(0)
	stalePolls := 0
	target := first.Target
	if target <= 0 {
		target = 6
	}

	report := func() {
		if opt.OnProgress == nil {
			return
		}
		elapsed := time.Since(lastAt).Seconds()
		speed := int64(0)
		if elapsed >= 0.25 {
			speed = int64(float64(doneBytes-lastBytes) / elapsed)
			lastBytes = doneBytes
			lastAt = time.Now()
		}
		// Live has no known total; show downloaded bytes as both sides.
		opt.OnProgress(doneBytes, doneBytes, speed)
	}

	appendSeg := func(s hls.Segment) error {
		if _, ok := seen[s.MediaSeq]; ok {
			return nil
		}
		u, err := hls.ResolveURL(mediaURL, s.URI)
		if err != nil {
			return err
		}
		keyURL := ""
		if s.KeyURI != "" {
			keyURL, err = hls.ResolveURL(mediaURL, s.KeyURI)
			if err != nil {
				return err
			}
		}
		tmp := filepath.Join(opt.Dir, fmt.Sprintf(".xdl-live-%d.ts", s.MediaSeq))
		n, err := d.fetchSeg(ctx, opt, segJob{
			URL: u, KeyURL: keyURL, KeyIV: s.KeyIV, MediaSeq: s.MediaSeq,
			ByteStart: s.ByteStart, ByteEnd: s.ByteEnd,
		}, tmp, keyCache)
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		f, err := os.Open(tmp)
		if err != nil {
			return err
		}
		copied, err := io.Copy(out, f)
		_ = f.Close()
		_ = os.Remove(tmp)
		if err != nil {
			return err
		}
		seen[s.MediaSeq] = struct{}{}
		doneBytes += copied
		if n > 0 && copied == 0 {
			doneBytes += n
		}
		report()
		return nil
	}

	process := func(pl *hls.Media) (ended bool, err error) {
		if pl.Target > 0 {
			target = pl.Target
		}
		added := 0
		for _, s := range pl.Segments {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			before := len(seen)
			if err := appendSeg(s); err != nil {
				return false, err
			}
			if len(seen) > before {
				added++
			}
		}
		if added == 0 {
			stalePolls++
		} else {
			stalePolls = 0
		}
		return !pl.Live, nil
	}

	if ended, err := process(first); err != nil {
		return nil, err
	} else if ended {
		return d.finalizeLive(ctx, opt, meta, finalPath, part, name, doneBytes, mediaURL)
	}

	probe := d.probe
	for {
		wait := time.Duration(target) * time.Second
		if wait < 2*time.Second {
			wait = 2 * time.Second
		}
		if wait > 15*time.Second {
			wait = 15 * time.Second
		}
		select {
		case <-ctx.Done():
			// Stopping a live record finalizes whatever was captured.
			_ = out.Sync()
			_ = out.Close()
			if st, e := os.Stat(part); e == nil && st.Size() > 0 {
				return d.finalizeLive(context.Background(), opt, meta, finalPath, part, name, st.Size(), mediaURL)
			}
			return nil, ctx.Err()
		case <-time.After(wait):
		}

		body, final, err := fetchText(ctx, probe, mediaURL, opt.Headers)
		if err != nil {
			if stalePolls > 8 {
				return nil, err
			}
			stalePolls++
			continue
		}
		if final != "" {
			mediaURL = final
			meta.MediaURL = mediaURL
		}
		pl, err := hls.ParseMedia(strings.NewReader(body))
		if err != nil {
			if stalePolls > 8 {
				return nil, err
			}
			stalePolls++
			continue
		}
		ended, err := process(pl)
		if err != nil {
			return nil, err
		}
		if ended || stalePolls > 40 {
			break
		}
	}

	return d.finalizeLive(ctx, opt, meta, finalPath, part, name, doneBytes, mediaURL)
}

func (d *Downloader) finalizeLive(ctx context.Context, opt Options, meta Meta, finalPath, part, name string, size int64, mediaURL string) (*Result, error) {
	_ = os.Rename(part, finalPath)
	st, err := os.Stat(finalPath)
	if err == nil {
		size = st.Size()
	}
	// Optional remux to MP4 when ffmpeg is available.
	mp4Path := finalPath
	if filepath.Ext(finalPath) == ".ts" {
		mp4Path = finalPath[:len(finalPath)-3] + ".mp4"
		ok, muxErr := remux.RemuxTSToMP4(ctx, mp4Path, finalPath, opt.FFmpegPath)
		if muxErr == nil && ok {
			_ = os.Remove(finalPath)
			finalPath = mp4Path
			name = filepath.Base(mp4Path)
			if st, e := os.Stat(finalPath); e == nil {
				size = st.Size()
			}
		}
	}
	meta.Live = false
	if opt.OnMeta != nil {
		_ = opt.OnMeta(meta)
	}
	if opt.OnProgress != nil {
		opt.OnProgress(size, size, 0)
	}
	return &Result{Path: finalPath, Size: size, FinalURL: mediaURL, Name: name}, nil
}
