package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/xdl-app/xdl/internal/netx"
)

const (
	DefaultConns     = 8
	MaxConnsNormal   = 16
	MaxConnsTurbo    = 32
	readBufSize      = 128 * 1024
	writeBufSize    = 1 << 20 // coalesce WriteAt into 1 MiB batches
	maxChunkRetries = 6
	progressInterval = 250 * time.Millisecond
	// readIdleTimeout bounds how long a chunk read may stall without progress.
	readIdleTimeout = 45 * time.Second
)

// readBufPool recycles the per-read transfer buffers. A buffer never outlives
// the goroutine that borrowed it, so returning it on every exit path is safe.
// *[]byte is pooled instead of []byte to avoid re-allocating the slice header.
var readBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, readBufSize)
		return &b
	},
}

var writeBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, writeBufSize)
		return &b
	},
}

func getReadBuf() *[]byte  { return readBufPool.Get().(*[]byte) }
func putReadBuf(b *[]byte) { readBufPool.Put(b) }

func getWriteBuf() *[]byte  { return writeBufPool.Get().(*[]byte) }
func putWriteBuf(b *[]byte) { writeBufPool.Put(b) }

// readController enforces a per-read idle deadline on a response body.
//
// ResponseHeaderTimeout only covers headers; once the body streams, a silently
// dead connection would block a worker forever while the 250ms progress ticker
// keeps reporting, i.e. a live-looking but frozen download.
//
// Client responses expose no deadline API directly, but the net/http body
// forwards SetReadDeadline to the underlying connection when it supports it.
// We probe for that method once; when it is absent we simply do nothing, so
// behaviour degrades to the previous (no deadline) path.
type readController struct {
	body     io.ReadCloser
	every    time.Duration
	last     time.Time
	supported bool
}

// readDeadliner is implemented by net/http response bodies that can forward a
// read deadline to their connection (Go 1.21+ transport).
type readDeadliner interface {
	SetReadDeadline(t time.Time) error
}

func newReadController(body io.ReadCloser) *readController {
	_, ok := body.(readDeadliner)
	return &readController{body: body, every: readIdleTimeout, supported: ok}
}

// refresh installs a fresh deadline, at most once per half-interval to keep the
// per-read cost negligible.
func (c *readController) refresh() error {
	if !c.supported {
		return nil
	}
	now := time.Now()
	if !c.last.IsZero() && now.Sub(c.last) < c.every/2 {
		return nil
	}
	c.last = now
	d, ok := c.body.(readDeadliner)
	if !ok {
		c.supported = false
		return nil
	}
	if err := d.SetReadDeadline(now.Add(c.every)); err != nil {
		c.supported = false
		return nil
	}
	return nil
}

// clear removes the deadline so a pooled connection is not reused with a
// stale one still attached.
func (c *readController) clear() {
	if !c.supported {
		return
	}
	if d, ok := c.body.(readDeadliner); ok {
		_ = d.SetReadDeadline(time.Time{})
	}
}

type DownloadOptions struct {
	URL        string
	Dir        string
	Name       string
	Headers    http.Header
	Conns      int
	Turbo      bool
	GlobalLim  *Limiter
	TaskLim    *Limiter
	OnProgress func(done, total, speed int64)

	// Resume / persistence (optional; Manager fills these).
	FinalPath    string
	PartPath     string
	ResumeChunks []Chunk
	ReusePart    bool // open existing part without truncating
	// OnProbe is called after a successful Probe. It may update FinalPath/PartPath/
	// ResumeChunks/ReusePart on the options pointer before download continues.
	OnProbe        func(probe *ProbeResult, opt *DownloadOptions) error
	OnChunksUpdate func([]Chunk)
}

type DownloadResult struct {
	Path     string
	Size     int64
	FinalURL string
	Name     string
}

// HTTPDownloader downloads one file with multi-connection Range + work stealing.
type HTTPDownloader struct {
	probeClient *http.Client
	dataClient  *http.Client
}

func NewHTTPDownloader() *HTTPDownloader {
	return &HTTPDownloader{
		probeClient: netx.NewProbeClient(30 * time.Second),
		dataClient:  netx.NewDataClient(60 * time.Second),
	}
}

func (d *HTTPDownloader) Download(ctx context.Context, opt DownloadOptions) (*DownloadResult, error) {
	if opt.Conns <= 0 {
		opt.Conns = DefaultConns
	}
	maxConns := MaxConnsNormal
	if opt.Turbo {
		maxConns = MaxConnsTurbo
	}
	if opt.Conns > maxConns {
		opt.Conns = maxConns
	}

	probe, err := Probe(ctx, d.probeClient, opt.URL, opt.Headers)
	if err != nil {
		return nil, err
	}
	if opt.OnProbe != nil {
		if err := opt.OnProbe(probe, &opt); err != nil {
			return nil, err
		}
	}

	name := opt.Name
	if name == "" {
		name = probe.Filename
	}
	name = SanitizeFilename(name)
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, err
	}

	finalPath := opt.FinalPath
	partPath := opt.PartPath
	if finalPath == "" {
		var uerr error
		finalPath, uerr = UniquePath(opt.Dir, name)
		if uerr != nil {
			return nil, uerr
		}
		opt.FinalPath = finalPath
	}
	if partPath == "" {
		partPath = PartPath(finalPath)
		opt.PartPath = partPath
	}

	reuse := opt.ReusePart
	if reuse {
		st, serr := os.Stat(LongPath(partPath))
		if serr != nil || (probe.Size > 0 && st.Size() != probe.Size) {
			reuse = false
			opt.ReusePart = false
			opt.ResumeChunks = nil
		}
	}

	if probe.Size > 0 {
		if err := EnsureDiskSpace(opt.Dir, probe.Size); err != nil {
			return nil, err
		}
		if !reuse {
			if err := Preallocate(partPath, probe.Size); err != nil {
				return nil, fmt.Errorf("preallocate: %w", err)
			}
		}
	}

	file, err := os.OpenFile(LongPath(partPath), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}

	single := probe.SingleStream || !probe.AcceptRanges || probe.Size <= 0 || probe.Size < MinChunkSize
	if single {
		err = d.downloadSingle(ctx, file, probe, opt)
	} else {
		err = d.downloadMulti(ctx, file, probe, opt)
	}

	syncErr := file.Sync()
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if syncErr != nil {
		return nil, syncErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	if err := os.Rename(LongPath(partPath), LongPath(finalPath)); err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}

	size := probe.Size
	if size <= 0 {
		if st, e := os.Stat(LongPath(finalPath)); e == nil {
			size = st.Size()
		}
	}

	return &DownloadResult{
		Path:     finalPath,
		Size:     size,
		FinalURL: probe.FinalURL,
		Name:     name,
	}, nil
}

// gate is a shared cool-off gate for overlapping connections.
//
// Opening all connections at once is what a downloader wants, but it can also
// trip per-IP or per-account limits. Rather than preventing that with a fixed
// ramp schedule (which just delays every download, whether or not it was
// needed), the gate idles until the server tells us to back off: any 429 pauses
// all workers for the interval from Retry-After, or a short default.
type gate struct {
	mu       sync.Mutex
	cooldown time.Duration
}

const defaultGateBackoff = 2 * time.Second

func newGate() *gate { return &gate{} }

// trip extends the cool-off. resetAfter is typically parsed Retry-After.
// A nil gate is inert, so callers may pass nil when no throttling is wanted.
func (g *gate) trip(resetAfter time.Duration) {
	if g == nil {
		return
	}
	d := resetAfter
	if d <= 0 {
		d = defaultGateBackoff
	}
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	g.mu.Lock()
	if d > g.cooldown {
		g.cooldown = d
	}
	g.mu.Unlock()
}

// wait blocks for the active cool-off, if any, honouring ctx cancellation.
func (g *gate) wait(ctx context.Context) {
	if g == nil {
		return
	}
	g.mu.Lock()
	d := g.cooldown
	g.cooldown = 0
	g.mu.Unlock()
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (d *HTTPDownloader) downloadSingle(ctx context.Context, file *os.File, probe *ProbeResult, opt DownloadOptions) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.FinalURL, nil)
	if err != nil {
		return err
	}
	copyHeaders(req.Header, opt.Headers)
	req.Header.Set("Accept-Encoding", "identity")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUA)
	}

	resp, err := d.dataClient.Do(req)
	if err != nil {
		return NewTaskError(ErrCodeNetwork, err.Error(), "检查网络后重试")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return classifyHTTP(resp.StatusCode)
	}

	bufp := getReadBuf()
	defer putReadBuf(bufp)
	buf := *bufp
	wbufp := getWriteBuf()
	defer putWriteBuf(wbufp)
	wbuf := *wbufp
	var written int64
	var pending int
	var lastReport time.Time
	windowStart := time.Now()
	var windowBytes int64

	flush := func() error {
		if pending == 0 {
			return nil
		}
		if err := waitLimiters(ctx, opt, pending); err != nil {
			return err
		}
		if _, err := file.WriteAt(wbuf[:pending], written); err != nil {
			return err
		}
		written += int64(pending)
		windowBytes += int64(pending)
		pending = 0
		return nil
	}

	rc := newReadController(resp.Body)
	// Registered after Close so the deadline is cleared before the body is
	// closed and its connection returns to the idle pool.
	defer rc.clear()
	for {
		if err := rc.refresh(); err != nil {
			_ = flush()
			return err
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			off := 0
			for off < n {
				room := len(wbuf) - pending
				take := n - off
				if take > room {
					take = room
				}
				copy(wbuf[pending:pending+take], buf[off:off+take])
				pending += take
				off += take
				if pending >= len(wbuf) {
					if err := flush(); err != nil {
						return err
					}
					if opt.OnProgress != nil && time.Since(lastReport) >= progressInterval {
						elapsed := time.Since(windowStart).Seconds()
						var spd int64
						if elapsed > 0 {
							spd = int64(float64(windowBytes) / elapsed)
						}
						opt.OnProgress(written, probe.Size, spd)
						lastReport = time.Now()
						windowStart = time.Now()
						windowBytes = 0
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = flush()
			return NewTaskError(ErrCodeNetwork, readErr.Error(), "连接中断，可断点续传重试")
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if opt.OnProgress != nil {
		opt.OnProgress(written, probe.Size, 0)
	}
	if probe.Size > 0 && written != probe.Size {
		return NewTaskError(ErrCodeNetwork, fmt.Sprintf("写入 %d 字节，声明 %d", written, probe.Size), "响应不完整，已保留分片文件")
	}
	return nil
}

func (d *HTTPDownloader) downloadMulti(ctx context.Context, file *os.File, probe *ProbeResult, opt DownloadOptions) error {
	var plan *ChunkPlan
	if len(opt.ResumeChunks) > 0 {
		var err error
		plan, err = LoadChunkPlan(opt.ResumeChunks, probe.Size)
		if err != nil {
			plan = NewChunkPlan(probe.Size, opt.Conns)
		}
	} else {
		plan = NewChunkPlan(probe.Size, opt.Conns)
	}
	emitChunks := func() {
		if opt.OnChunksUpdate != nil {
			opt.OnChunksUpdate(plan.Snapshot())
		}
	}
	emitChunks()

	var statsMu sync.Mutex
	stats := make(map[int]int64)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer emitChunks()

	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	var startMu sync.Mutex
	started := 0
	gate := newGate()

	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		var lastDone int64
		lastT := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				done := plan.TotalDone()
				elapsed := time.Since(lastT).Seconds()
				var spd int64
				if elapsed > 0 {
					spd = int64(float64(done-lastDone) / elapsed)
				}
				lastDone, lastT = done, time.Now()
				if opt.OnProgress != nil {
					opt.OnProgress(done, probe.Size, spd)
				}
				// Keep the in-memory chunk snapshot fresh for pause/resume.
				// Disk persistence is separately rate-limited in Manager.
				emitChunks()
				if plan.AllDone() {
					return
				}
			}
		}
	}()

	runWorker := func(workerID int) {
		defer wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			// Honour a server-signalled cool-off before opening a new request.
			gate.wait(ctx)
			chunk := plan.ClaimNext(workerID)
			if chunk == nil {
				statsMu.Lock()
				spdCopy := make(map[int]int64, len(stats))
				for k, v := range stats {
					spdCopy[k] = v
				}
				statsMu.Unlock()
				chunk = plan.TrySteal(workerID, spdCopy)
				if chunk == nil {
					if !plan.HasPendingOrRunning() {
						return
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(100 * time.Millisecond):
						continue
					}
				}
			}

			err := d.fetchChunk(ctx, file, probe, opt, plan, chunk, gate, func(spd int64) {
				statsMu.Lock()
				stats[chunk.Index] = spd
				statsMu.Unlock()
			})
			if err != nil {
				if te, ok := err.(*TaskError); ok && (te.Code == ErrCodeRangeRejected || te.Code == ErrCodeValidatorChange) {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				attempts := chunk.Attempts + 1
				if attempts >= maxChunkRetries {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
				plan.MarkFailed(chunk.Index)
				select {
				case <-ctx.Done():
					return
				case <-time.After(retryBackoff(attempts)):
					continue
				}
			}
			plan.MarkDone(chunk.Index)
		}
	}

	launch := func(id int) {
		startMu.Lock()
		started++
		startMu.Unlock()
		wg.Add(1)
		go runWorker(id)
	}

	// Start every worker immediately. The previous implementation opened only 4
	// connections and added one every 2s, so reaching the configured width took
	// 8s at the default 8 conns and 56s in turbo mode — crippling short and
	// medium downloads, which are the common case. Overlapping connections are
	// now guarded by `gate`, which backs off on 429/Retry-After instead of
	// guessing a fixed ramp schedule.
	for i := 0; i < opt.Conns; i++ {
		launch(i)
	}

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		cancel()
		<-progressDone
		if !plan.AllDone() {
			select {
			case err := <-errCh:
				return err
			default:
				return NewTaskError(ErrCodeInternal, "下载未完成", "请重试")
			}
		}
		if err := plan.ValidateComplete(probe.Size); err != nil {
			return NewTaskError(ErrCodeInternal, err.Error(), "分片未完整覆盖文件")
		}
		return nil
	case err := <-errCh:
		cancel()
		<-doneCh
		<-progressDone
		return err
	}
}

func (d *HTTPDownloader) fetchChunk(
	ctx context.Context,
	file *os.File,
	probe *ProbeResult,
	opt DownloadOptions,
	plan *ChunkPlan,
	chunk *Chunk,
	gate *gate,
	onSpeed func(int64),
) error {
	from := chunk.Start + chunk.Done
	to := chunk.End
	if from > to {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.FinalURL, nil)
	if err != nil {
		return err
	}
	copyHeaders(req.Header, opt.Headers)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUA)
	}
	if probe.ETag != "" {
		req.Header.Set("If-Range", probe.ETag)
	} else if probe.LastModified != "" {
		req.Header.Set("If-Range", probe.LastModified)
	}

	resp, err := d.dataClient.Do(req)
	if err != nil {
		return NewTaskError(ErrCodeNetwork, err.Error(), "分片连接失败，将自动重试")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return NewTaskError(ErrCodeRangeRejected, "服务器忽略 Range", "将降级为单连接")
	case http.StatusPartialContent:
		if err := checkContentRange(resp.Header.Get("Content-Range"), from, to, probe.Size); err != nil {
			return err
		}
	case 429, 503:
		// Back the whole connection set off, not just this chunk.
		gate.trip(parseRetryAfter(resp.Header.Get("Retry-After")))
		return NewTaskError(ErrCodeNetwork, "请求过于频繁", "自动降速重试")
	default:
		return classifyHTTP(resp.StatusCode)
	}

	bufp := getReadBuf()
	defer putReadBuf(bufp)
	buf := *bufp
	wbufp := getWriteBuf()
	defer putWriteBuf(wbufp)
	wbuf := *wbufp

	offset := from
	downloaded := chunk.Done
	var pending int // bytes buffered in wbuf starting at `offset`
	windowStart := time.Now()
	var windowBytes int64

	_, curEnd, ok := plan.CurrentEnd(chunk.Index)
	if !ok {
		return NewTaskError(ErrCodeInternal, "分片不存在", "请重试")
	}

	// flush writes coalesced bytes, never past curEnd (TrySteal may shrink it).
	flush := func() error {
		if pending > 0 {
			maxWrite := curEnd - offset + 1
			if maxWrite <= 0 {
				pending = 0
			} else {
				writeN := pending
				if int64(writeN) > maxWrite {
					writeN = int(maxWrite)
				}
				if err := waitLimiters(ctx, opt, writeN); err != nil {
					return err
				}
				if _, err := file.WriteAt(wbuf[:writeN], offset); err != nil {
					return err
				}
				offset += int64(writeN)
				downloaded = offset - chunk.Start
				windowBytes += int64(writeN)
				pending = 0
			}
		}
		var ok2 bool
		curEnd, ok2 = plan.UpdateProgressBounds(chunk.Index, downloaded)
		if !ok2 {
			return NewTaskError(ErrCodeInternal, "分片不存在", "请重试")
		}
		if elapsed := time.Since(windowStart).Seconds(); elapsed >= 0.5 && onSpeed != nil && windowBytes > 0 {
			onSpeed(int64(float64(windowBytes) / elapsed))
			windowStart = time.Now()
			windowBytes = 0
		}
		return nil
	}

	rc := newReadController(resp.Body)
	defer rc.clear()

	for {
		if err := rc.refresh(); err != nil {
			_ = flush()
			return err
		}
		if offset > curEnd {
			_ = flush()
			return nil
		}

		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// Re-read bounds: TrySteal may have shrunk End during Read.
			_, curEnd, ok = plan.CurrentEnd(chunk.Index)
			if !ok {
				_ = flush()
				return NewTaskError(ErrCodeInternal, "分片不存在", "请重试")
			}
			remain := curEnd - offset - int64(pending) + 1
			if remain <= 0 {
				if err := flush(); err != nil {
					return err
				}
				return nil
			}
			take := n
			if int64(take) > remain {
				take = int(remain)
			}
			src := buf[:take]
			for len(src) > 0 {
				room := len(wbuf) - pending
				chunkN := len(src)
				if chunkN > room {
					chunkN = room
				}
				copy(wbuf[pending:pending+chunkN], src[:chunkN])
				pending += chunkN
				src = src[chunkN:]
				if pending >= len(wbuf) {
					if err := flush(); err != nil {
						return err
					}
					if offset > curEnd {
						return nil
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = flush()
			return NewTaskError(ErrCodeNetwork, readErr.Error(), "分片中断，将重试")
		}
	}

	if err := flush(); err != nil {
		return err
	}
	need := curEnd - from + 1
	if need < 0 {
		need = 0
	}
	if offset-from < need {
		plan.UpdateProgress(chunk.Index, downloaded)
		return NewTaskError(ErrCodeNetwork, "分片不完整", "将重试该分片")
	}
	return nil
}

func waitLimiters(ctx context.Context, opt DownloadOptions, n int) error {
	if opt.TaskLim != nil {
		if err := opt.TaskLim.WaitN(ctx, n); err != nil {
			return err
		}
	}
	if opt.GlobalLim != nil {
		if err := opt.GlobalLim.WaitN(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

func retryBackoff(attempt int) time.Duration {
	base := 500 * time.Millisecond
	d := base << attempt
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	jitter := time.Duration(time.Now().UnixNano() % int64(d/2+1))
	return d/2 + jitter
}

func checkContentRange(header string, from, to, size int64) error {
	start, end, total, star, ok := parseContentRange(header)
	if !ok {
		return NewTaskError(ErrCodeValidatorChange, "缺少或无法解析 Content-Range", "重新探测资源")
	}
	if start != from || end != to {
		return NewTaskError(ErrCodeValidatorChange,
			fmt.Sprintf("Content-Range %d-%d 与请求 %d-%d 不符", start, end, from, to),
			"重新探测资源")
	}
	if !star && size > 0 && total != size {
		return NewTaskError(ErrCodeValidatorChange,
			fmt.Sprintf("Content-Range 总长 %d 与探测 %d 不符", total, size),
			"重新探测资源")
	}
	return nil
}

func classifyHTTP(code int) error {
	switch code {
	case 401:
		return NewTaskError(ErrCodeAuthRequired, "需要登录", "刷新 Cookie")
	case 403:
		return NewTaskError(ErrCodeForbidden, "拒绝访问", "检查权限")
	case 404:
		return NewTaskError(ErrCodeNotFound, "资源不存在", "确认链接")
	case http.StatusRequestedRangeNotSatisfiable:
		return NewTaskError(ErrCodeValidatorChange, "Range 无效", "重新探测资源")
	default:
		return NewTaskError(ErrCodeNetwork, fmt.Sprintf("HTTP %d", code), "稍后重试")
	}
}
