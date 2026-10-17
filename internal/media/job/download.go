package job

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xdl-app/xdl/internal/media/dash"
	"github.com/xdl-app/xdl/internal/media/hls"
	"github.com/xdl-app/xdl/internal/media/remux"
	"github.com/xdl-app/xdl/internal/netx"
)

// RateLimiter is the subset of the engine limiter used while reading segments.
type RateLimiter interface {
	WaitN(ctx context.Context, n int) error
}

const (
	defaultSegConns = 6
	maxSegConns     = 8
	maxSegRetries   = 6
)

// Options controls a media download run.
type Options struct {
	URL        string
	Dir        string
	Name       string
	Headers    http.Header
	Meta       Meta
	Conns      int
	FFmpegPath string
	GlobalLim  RateLimiter
	FinalPath  string
	OnProgress func(done, total, speed int64)
	OnMeta     func(Meta) error
}

// Result is the finished media file.
type Result struct {
	Path     string
	Size     int64
	FinalURL string
	Name     string
}

// Downloader downloads HLS/DASH VOD segments then remuxes.
type Downloader struct {
	client *http.Client
}

func NewDownloader() *Downloader {
	return &Downloader{client: netx.NewDataClient(60 * time.Second)}
}

func (d *Downloader) Download(ctx context.Context, opt Options) (*Result, error) {
	if opt.Conns <= 0 {
		opt.Conns = defaultSegConns
	}
	if opt.Conns > maxSegConns {
		opt.Conns = maxSegConns
	}
	meta := opt.Meta
	if meta.Protocol == "" {
		meta.Protocol = DetectKind(opt.URL)
	}
	switch meta.Protocol {
	case "hls":
		return d.downloadHLS(ctx, opt, meta)
	case "dash":
		return d.downloadDASH(ctx, opt, meta)
	default:
		return nil, fmt.Errorf("未知媒体协议：仅支持 HLS / DASH VOD")
	}
}

type segJob struct {
	Index     int
	URL       string
	KeyURL    string
	KeyIV     string
	MediaSeq  uint64
	ByteStart int64
	ByteEnd   int64
}

func (d *Downloader) downloadHLS(ctx context.Context, opt Options, meta Meta) (*Result, error) {
	mediaURL := meta.MediaURL
	if mediaURL == "" {
		mediaURL = opt.URL
	}
	body, finalURL, err := fetchText(ctx, netx.NewProbeClient(30*time.Second), mediaURL, opt.Headers)
	if err != nil {
		return nil, err
	}
	pl, err := hls.ParseMedia(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	mediaURL = finalURL
	meta.MediaURL = mediaURL
	meta.Protocol = "hls"
	if pl.IsFMP4 {
		meta.Container = "fmp4"
	} else {
		meta.Container = "ts"
	}

	segs := make([]segJob, 0, len(pl.Segments))
	for i, s := range pl.Segments {
		u, err := hls.ResolveURL(mediaURL, s.URI)
		if err != nil {
			return nil, err
		}
		keyURL := ""
		if s.KeyURI != "" {
			keyURL, err = hls.ResolveURL(mediaURL, s.KeyURI)
			if err != nil {
				return nil, err
			}
		}
		segs = append(segs, segJob{
			Index: i, URL: u, KeyURL: keyURL, KeyIV: s.KeyIV,
			MediaSeq: s.MediaSeq, ByteStart: s.ByteStart, ByteEnd: s.ByteEnd,
		})
	}
	var initURL string
	var initStart, initEnd int64 = -1, -1
	if pl.MapURI != "" {
		u, err := hls.ResolveURL(mediaURL, pl.MapURI)
		if err != nil {
			return nil, err
		}
		initURL = u
		meta.InitURL = u
		initStart, initEnd = pl.MapByteStart, pl.MapByteEnd
	}
	return d.run(ctx, opt, meta, initURL, initStart, initEnd, segs, "", nil)
}

func (d *Downloader) downloadDASH(ctx context.Context, opt Options, meta Meta) (*Result, error) {
	body, finalURL, err := fetchText(ctx, netx.NewProbeClient(30*time.Second), opt.URL, opt.Headers)
	if err != nil {
		return nil, err
	}
	man, err := dash.Parse(strings.NewReader(body), finalURL)
	if err != nil {
		return nil, err
	}
	var chosen *dash.Variant
	for i := range man.Variants {
		v := &man.Variants[i]
		if meta.VariantID != "" && (v.ID == meta.VariantID || fmt.Sprintf("%d", i) == meta.VariantID) {
			chosen = v
			break
		}
		if meta.Label != "" && v.Label == meta.Label {
			chosen = v
			break
		}
	}
	if chosen == nil {
		best := &man.Variants[0]
		for i := range man.Variants {
			if man.Variants[i].Bandwidth > best.Bandwidth {
				best = &man.Variants[i]
			}
		}
		chosen = best
	}
	meta.Protocol = "dash"
	meta.Container = "fmp4"
	meta.MediaURL = finalURL
	meta.VariantID = chosen.ID
	meta.Label = chosen.Label
	meta.Bandwidth = chosen.Bandwidth
	meta.Width = chosen.Width
	meta.Height = chosen.Height
	meta.InitURL = chosen.InitURL
	meta.AudioInit = chosen.AudioInit
	meta.AudioMedia = append([]string(nil), chosen.AudioMedia...)

	segs := make([]segJob, 0, len(chosen.MediaURLs))
	for i, u := range chosen.MediaURLs {
		segs = append(segs, segJob{Index: i, URL: u, ByteStart: -1, ByteEnd: -1})
	}
	audioSegs := make([]segJob, 0, len(chosen.AudioMedia))
	for i, u := range chosen.AudioMedia {
		audioSegs = append(audioSegs, segJob{Index: i, URL: u, ByteStart: -1, ByteEnd: -1})
	}
	return d.run(ctx, opt, meta, chosen.InitURL, -1, -1, segs, chosen.AudioInit, audioSegs)
}

func (d *Downloader) run(ctx context.Context, opt Options, meta Meta, initURL string, initStart, initEnd int64, segs []segJob, audioInit string, audioSegs []segJob) (*Result, error) {
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, err
	}
	name := opt.Name
	if name == "" {
		ext := ".ts"
		if meta.Container == "fmp4" {
			ext = ".mp4"
		}
		if meta.Protocol == "dash" {
			name = dash.SuggestName(opt.URL, meta.Label)
		} else {
			name = hls.SuggestName(opt.URL, meta.Label, ext)
		}
	}
	name = sanitizeLeaf(name)

	finalPath := opt.FinalPath
	if finalPath == "" {
		var err error
		finalPath, err = uniquePath(opt.Dir, name)
		if err != nil {
			return nil, err
		}
	}
	workDir := meta.WorkDir
	if workDir == "" {
		workDir = finalPath + ".xdlmedia"
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	meta.WorkDir = workDir
	if len(meta.SegDone) != len(segs) {
		meta.SegDone = make([]bool, len(segs))
	}
	if len(audioSegs) > 0 && len(meta.AudioDone) != len(audioSegs) {
		meta.AudioDone = make([]bool, len(audioSegs))
	}
	if opt.OnMeta != nil {
		if err := opt.OnMeta(meta); err != nil {
			return nil, err
		}
	}

	keyCache := &sync.Map{}
	var doneBytes int64
	var speedMu sync.Mutex
	lastBytes := int64(0)
	lastAt := time.Now()

	report := func() {
		if opt.OnProgress == nil {
			return
		}
		done := atomic.LoadInt64(&doneBytes)
		speedMu.Lock()
		elapsed := time.Since(lastAt).Seconds()
		speed := int64(0)
		if elapsed >= 0.2 {
			speed = int64(float64(done-lastBytes) / elapsed)
			lastBytes = done
			lastAt = time.Now()
		}
		speedMu.Unlock()
		completed := 0
		for _, ok := range meta.SegDone {
			if ok {
				completed++
			}
		}
		total := done
		pending := len(segs) - completed
		if completed > 0 && pending > 0 {
			avg := done / int64(completed)
			if avg < 1 {
				avg = 1
			}
			total = done + avg*int64(pending)
		} else if pending > 0 {
			total = int64(len(segs)) * 256 * 1024
		}
		if total < done {
			total = done
		}
		opt.OnProgress(done, total, speed)
	}

	fetchOne := func(seg segJob, dest string) (int64, error) {
		return d.fetchSeg(ctx, opt, seg, dest, keyCache)
	}

	if initURL != "" && !meta.InitDone {
		n, err := fetchOne(segJob{URL: initURL, ByteStart: initStart, ByteEnd: initEnd}, filepath.Join(workDir, "init.mp4"))
		if err != nil {
			return nil, err
		}
		atomic.AddInt64(&doneBytes, n)
		meta.InitDone = true
		if opt.OnMeta != nil {
			_ = opt.OnMeta(meta)
		}
		report()
	}

	if err := d.fetchParallel(ctx, opt, segs, meta.SegDone, func(i int) string {
		return segPath(workDir, i, meta.Container)
	}, &doneBytes, keyCache, func() {
		if opt.OnMeta != nil {
			_ = opt.OnMeta(meta)
		}
		report()
	}); err != nil {
		return nil, err
	}

	if audioInit != "" {
		if _, err := fetchOne(segJob{URL: audioInit, ByteStart: -1, ByteEnd: -1}, filepath.Join(workDir, "audio_init.mp4")); err != nil {
			return nil, err
		}
	}
	if len(audioSegs) > 0 {
		if err := d.fetchParallel(ctx, opt, audioSegs, meta.AudioDone, func(i int) string {
			return filepath.Join(workDir, fmt.Sprintf("a_%05d.m4s", i))
		}, &doneBytes, keyCache, func() {
			if opt.OnMeta != nil {
				_ = opt.OnMeta(meta)
			}
			report()
		}); err != nil {
			return nil, err
		}
	}

	part := finalPath + ".xdlpart"
	_ = os.Remove(part)

	switch meta.Container {
	case "fmp4":
		mediaPaths := make([]string, len(segs))
		for i := range segs {
			mediaPaths[i] = segPath(workDir, i, meta.Container)
		}
		initPath := ""
		if initURL != "" {
			initPath = filepath.Join(workDir, "init.mp4")
		}
		videoPart := part
		hasAudio := len(audioSegs) > 0
		if hasAudio {
			videoPart = filepath.Join(workDir, "video_only.mp4")
		}
		if err := remux.JoinFMP4(videoPart, initPath, mediaPaths); err != nil {
			return nil, err
		}
		if hasAudio {
			audioPaths := make([]string, len(audioSegs))
			for i := range audioSegs {
				audioPaths[i] = filepath.Join(workDir, fmt.Sprintf("a_%05d.m4s", i))
			}
			audioJoined := filepath.Join(workDir, "audio_only.mp4")
			aInit := filepath.Join(workDir, "audio_init.mp4")
			if _, err := os.Stat(aInit); err != nil {
				aInit = ""
			}
			if err := remux.JoinFMP4(audioJoined, aInit, audioPaths); err != nil {
				return nil, err
			}
			ok, err := remux.MuxAV(ctx, part, videoPart, audioJoined, opt.FFmpegPath)
			if err != nil {
				return nil, err
			}
			if !ok {
				if err := copyFile(videoPart, part); err != nil {
					return nil, fmt.Errorf("缺少 ffmpeg，无法合并音轨：%w", err)
				}
			}
		}
	default:
		paths := make([]string, len(segs))
		for i := range segs {
			paths[i] = segPath(workDir, i, meta.Container)
		}
		tsPath := filepath.Join(workDir, "all.ts")
		if err := remux.ConcatFiles(tsPath, paths); err != nil {
			return nil, err
		}
		ok, err := remux.RemuxTSToMP4(ctx, part, tsPath, opt.FFmpegPath)
		if ok && err == nil {
			if !strings.HasSuffix(strings.ToLower(finalPath), ".mp4") {
				finalPath = strings.TrimSuffix(finalPath, filepath.Ext(finalPath)) + ".mp4"
			}
		} else {
			// No ffmpeg, or remux failed on atypical payloads — keep concatenated .ts.
			_ = os.Remove(part)
			if strings.HasSuffix(strings.ToLower(finalPath), ".mp4") {
				finalPath = strings.TrimSuffix(finalPath, filepath.Ext(finalPath)) + ".ts"
				part = finalPath + ".xdlpart"
			}
			if err := copyFile(tsPath, part); err != nil {
				return nil, err
			}
		}
	}

	st, err := os.Stat(part)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(part, finalPath); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(workDir)
	if opt.OnProgress != nil {
		opt.OnProgress(st.Size(), st.Size(), 0)
	}
	return &Result{
		Path:     finalPath,
		Size:     st.Size(),
		FinalURL: meta.MediaURL,
		Name:     filepath.Base(finalPath),
	}, nil
}

func (d *Downloader) fetchParallel(ctx context.Context, opt Options, segs []segJob, doneFlags []bool, pathFn func(int) string, doneBytes *int64, keyCache *sync.Map, onDone func()) error {
	type item struct {
		idx int
		seg segJob
	}
	pending := make([]item, 0, len(segs))
	for i, s := range segs {
		if i < len(doneFlags) && doneFlags[i] {
			if st, err := os.Stat(pathFn(i)); err == nil {
				atomic.AddInt64(doneBytes, st.Size())
			}
			continue
		}
		pending = append(pending, item{idx: i, seg: s})
	}
	if len(pending) == 0 {
		return nil
	}
	workers := opt.Conns
	if workers > len(pending) {
		workers = len(pending)
	}
	ch := make(chan item)
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	var metaMu sync.Mutex
	var failOnce sync.Once
	fail := func(err error) {
		failOnce.Do(func() { errCh <- err })
	}
	worker := func() {
		defer wg.Done()
		for it := range ch {
			if ctx.Err() != nil {
				fail(ctx.Err())
				return
			}
			n, err := d.fetchSeg(ctx, opt, it.seg, pathFn(it.idx), keyCache)
			if err != nil {
				fail(err)
				return
			}
			atomic.AddInt64(doneBytes, n)
			metaMu.Lock()
			if it.idx < len(doneFlags) {
				doneFlags[it.idx] = true
			}
			metaMu.Unlock()
			onDone()
		}
	}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}
	for _, it := range pending {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case ch <- it:
		}
	}
	close(ch)
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}
	return ctx.Err()
}

func segPath(workDir string, i int, container string) string {
	ext := ".ts"
	if container == "fmp4" {
		ext = ".m4s"
	}
	return filepath.Join(workDir, fmt.Sprintf("s_%05d%s", i, ext))
}

func (d *Downloader) fetchSeg(ctx context.Context, opt Options, seg segJob, dest string, keyCache *sync.Map) (int64, error) {
	var lastErr error
	for attempt := 0; attempt < maxSegRetries; attempt++ {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		n, err := d.fetchSegOnce(ctx, opt, seg, dest, keyCache)
		if err == nil {
			return n, nil
		}
		lastErr = err
		backoff := time.Duration(1<<attempt) * 400 * time.Millisecond
		if backoff > 8*time.Second {
			backoff = 8 * time.Second
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return 0, lastErr
}

func (d *Downloader) fetchSegOnce(ctx context.Context, opt Options, seg segJob, dest string, keyCache *sync.Map) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, seg.URL, nil)
	if err != nil {
		return 0, err
	}
	for k, vs := range opt.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if seg.ByteStart >= 0 && seg.ByteEnd >= seg.ByteStart {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", seg.ByteStart, seg.ByteEnd))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("分片 HTTP %d", resp.StatusCode)
	}
	var reader io.Reader = resp.Body
	if opt.GlobalLim != nil {
		reader = &limitedReader{ctx: ctx, r: resp.Body, lim: opt.GlobalLim}
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	if seg.KeyURL != "" {
		key, err := d.loadKey(ctx, opt, seg.KeyURL, keyCache)
		if err != nil {
			return 0, err
		}
		data, err = hls.DecryptAES128(data, key, seg.KeyIV, seg.MediaSeq)
		if err != nil {
			return 0, err
		}
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return int64(len(data)), nil
}

func (d *Downloader) loadKey(ctx context.Context, opt Options, keyURL string, cache *sync.Map) ([]byte, error) {
	if v, ok := cache.Load(keyURL); ok {
		return v.([]byte), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, keyURL, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range opt.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("密钥 HTTP %d", resp.StatusCode)
	}
	key, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return nil, err
	}
	if len(key) != 16 {
		return nil, fmt.Errorf("密钥长度无效：%d", len(key))
	}
	cache.Store(keyURL, key)
	return key, nil
}

type limitedReader struct {
	ctx context.Context
	r   io.Reader
	lim RateLimiter
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	if l.lim != nil {
		if err := l.lim.WaitN(l.ctx, len(p)); err != nil {
			return 0, err
		}
	}
	return l.r.Read(p)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func sanitizeLeaf(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "video"
	}
	repl := strings.NewReplacer("<", "_", ">", "_", ":", "_", "\"", "_", "/", "_", "\\", "_", "|", "_", "?", "_", "*", "_")
	name = repl.Replace(name)
	name = strings.Trim(name, " .")
	if name == "" {
		return "video"
	}
	return name
}

func uniquePath(dir, name string) (string, error) {
	base := filepath.Join(dir, name)
	if _, err := os.Stat(base); err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 10000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(cand); err != nil {
			if os.IsNotExist(err) {
				return cand, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("无法生成唯一文件名")
}
