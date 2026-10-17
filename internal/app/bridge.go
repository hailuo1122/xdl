package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xdl-app/xdl/internal/bittorrent"
	"github.com/xdl-app/xdl/internal/engine"
	"github.com/xdl-app/xdl/internal/ipc"
	"github.com/xdl-app/xdl/internal/media/job"
	"github.com/xdl-app/xdl/internal/nativehost"
	"github.com/xdl-app/xdl/internal/settings"
)

const Version = "0.0.5-m5"

// ErrAlreadyRunning means another healthy XDL instance owns the store/pipe.
// Browser wake (--minimized) hits this when the primary is alive; callers should exit 0.
var ErrAlreadyRunning = errors.New("already running")

// Runtime owns the engine, durable settings and the local RPC surface.
type Runtime struct {
	Mgr     *engine.Manager
	Srv     *ipc.Server
	dataDir string

	settingsMu sync.RWMutex
	settings   settings.Settings

	seenMu sync.Mutex
	seen   ExtensionSeen

	dedupeMu sync.Mutex
	dedupe   map[string]dedupeEntry

	chooseDirMu sync.Mutex
	chooseDir   func() (string, error)

	pickMu sync.Mutex
	picks  map[string]time.Time

	showMu sync.Mutex
	showFn func()

	ariaMu sync.Mutex
	aria   *ariaListener
}

// ExtensionSeen records the last native-host client observed (for UI status).
type ExtensionSeen struct {
	LastSeen time.Time `json:"lastSeen"`
	Version  string    `json:"version,omitempty"`
	Browser  string    `json:"browser,omitempty"`
	ID       string    `json:"id,omitempty"`
}

func DataDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDL_DATA_DIR")); dir != "" {
		return dir
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "XDL")
}

func NativeDir() string {
	return filepath.Join(DataDir(), "native")
}

func storeBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "locked") || strings.Contains(msg, "busy")
}

func Open() (*Runtime, error) {
	cfgDir := DataDir()
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return nil, err
	}
	dlDir := filepath.Join(cfgDir, "Downloads")
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		return nil, err
	}
	mgr, err := engine.NewManager(engine.ManagerConfig{
		DataDir:       cfgDir,
		DownloadDir:   dlDir,
		MaxConcurrent: 3,
		DefaultConns:  engine.DefaultConns,
	})
	if err != nil {
		if storeBusy(err) && ipc.Ping(500*time.Millisecond) == nil {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	s := settings.Load(mgr.Store(), cfgDir)
	if err := os.MkdirAll(s.DownloadDir, 0o755); err != nil {
		s.DownloadDir = dlDir
		_ = os.MkdirAll(dlDir, 0o755)
	}
	mgr.ApplyConfig(engine.RuntimeConfig{
		DownloadDir:   s.DownloadDir,
		MaxConcurrent: s.MaxConcurrent,
		DefaultConns:  s.DefaultConns,
		Turbo:         s.Turbo,
		GlobalLimit:   s.GlobalLimitBps,
		FFmpegPath:    s.FFmpegPath,
		BTMaxPeers:    s.BTMaxPeers,
		BTUploadLimit: s.BTUploadLimitBps,
		BTSeed:        s.BTSeedAfterDone,
	})
	rt := &Runtime{
		Mgr:      mgr,
		dataDir:  cfgDir,
		settings: s,
		dedupe:   make(map[string]dedupeEntry),
	}
	rt.Srv = ipc.NewServer(rt.handle)
	return rt, nil
}

func (rt *Runtime) Close() {
	rt.stopAria2()
	if rt.Srv != nil {
		_ = rt.Srv.Close()
	}
	if rt.Mgr != nil {
		_ = rt.Mgr.Close()
	}
}

func (rt *Runtime) Serve(ctx context.Context) {
	go func() {
		if err := rt.Srv.Start(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "pipe server:", err)
		}
	}()
	ch := rt.Mgr.Bus().Subscribe(64)
	go func() {
		for {
			select {
			case <-ctx.Done():
				rt.Mgr.Bus().Unsubscribe(ch)
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				rt.Srv.Broadcast(ipc.EventMessage{
					Method: "event." + string(ev.Type),
					Params: ev.Data,
				})
			}
		}
	}()
	rt.Mgr.Bus().Publish(engine.Event{Type: engine.EventAppReady, Data: map[string]string{"version": Version}})
	go rt.scheduleLoop(ctx)
}

func (rt *Runtime) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	held := false
	apply := func() {
		s := rt.Settings()
		pause := s.InSchedulePause(time.Now())
		if pause && !held {
			rt.Mgr.SetHold(true)
			rt.Mgr.PauseAll()
			held = true
		} else if !pause && held {
			rt.Mgr.SetHold(false)
			rt.Mgr.ResumeAll()
			held = false
		} else if pause {
			rt.Mgr.SetHold(true)
		}
	}
	apply()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			apply()
		}
	}
}

func (rt *Runtime) Settings() settings.Settings {
	rt.settingsMu.RLock()
	defer rt.settingsMu.RUnlock()
	return rt.settings
}

// SaveSettings persists and applies shell preferences.
func (rt *Runtime) SaveSettings(in settings.Settings) (settings.Settings, error) {
	s, err := settings.Save(rt.Mgr.Store(), in, rt.dataDir)
	if err != nil {
		return rt.Settings(), err
	}
	rt.settingsMu.Lock()
	rt.settings = s
	rt.settingsMu.Unlock()
	rt.Mgr.ApplyConfig(engine.RuntimeConfig{
		DownloadDir:   s.DownloadDir,
		MaxConcurrent: s.MaxConcurrent,
		DefaultConns:  s.DefaultConns,
		Turbo:         s.Turbo,
		GlobalLimit:   s.GlobalLimitBps,
		FFmpegPath:    s.FFmpegPath,
		BTMaxPeers:    s.BTMaxPeers,
		BTUploadLimit: s.BTUploadLimitBps,
		BTSeed:        s.BTSeedAfterDone,
	})
	rt.restartAria2()
	return s, nil
}

// TaskView is the safe, UI-facing projection of a task (headers stay private).
type TaskView struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	URL       string            `json:"url"`
	FinalURL  string            `json:"finalUrl,omitempty"`
	Name      string            `json:"name"`
	State     string            `json:"state"`
	Dir       string            `json:"dir,omitempty"`
	FinalPath string            `json:"finalPath,omitempty"`
	Size      int64             `json:"size"`
	Done      int64             `json:"done"`
	Speed     int64             `json:"speed"`
	Conns     int               `json:"conns"`
	Category  string            `json:"category,omitempty"`
	Validator engine.Validator  `json:"validator"`
	Err       *engine.TaskError `json:"err,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	// Torrent-only live stats from KindMeta.
	Peers    int     `json:"peers,omitempty"`
	Seeds    int     `json:"seeds,omitempty"`
	Uploaded int64   `json:"uploaded,omitempty"`
	Ratio    float64 `json:"ratio,omitempty"`
}

func toView(t *engine.Task) TaskView {
	if t == nil {
		return TaskView{}
	}
	v := TaskView{
		ID:        string(t.ID),
		Kind:      string(t.Kind),
		URL:       t.URL,
		FinalURL:  t.FinalURL,
		Name:      t.Name,
		State:     string(t.State),
		Dir:       t.Dir,
		FinalPath: t.FinalPath,
		Size:      t.Size,
		Done:      t.Done,
		Speed:     t.Speed,
		Conns:     t.Conns,
		Category:  t.Category,
		Validator: t.Validator,
		Err:       t.Err,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
	if t.Kind == engine.KindTorrent {
		meta := bittorrent.DecodeMeta(t.KindMeta)
		v.Peers = meta.Peers
		v.Seeds = meta.Seeds
		v.Uploaded = meta.Uploaded
		v.Ratio = meta.Ratio
		if v.Conns == 0 && meta.Peers > 0 {
			v.Conns = meta.Peers
		}
	}
	return v
}

// ListViews returns task projections sorted newest first.
func (rt *Runtime) ListViews() []TaskView {
	tasks := rt.Mgr.List()
	out := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toView(t))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// AddRequest is shared by the shell (trusted dir) and native host (sandboxed).
type AddRequest struct {
	URL           string            `json:"url"`
	Dir           string            `json:"dir,omitempty"`
	Name          string            `json:"name,omitempty"`
	Conns         int               `json:"conns,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	VariantID     string            `json:"variantId,omitempty"`
	Label         string            `json:"label,omitempty"`
	Referer       string            `json:"referer,omitempty"`
	UserAgent     string            `json:"userAgent,omitempty"`
	Cookie        string            `json:"cookie,omitempty"`
	Authorization string            `json:"authorization,omitempty"`
	Origin        string            `json:"origin,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	// Torrent fields (trusted shell / torrent.start).
	InfoHash    string `json:"infoHash,omitempty"`
	TorrentPath string `json:"torrentPath,omitempty"`
	Selected    []int  `json:"selected,omitempty"`
	SeedAfter   *bool  `json:"seedAfter,omitempty"`
	AcceptLegal bool   `json:"acceptLegal,omitempty"`
}

var headerAllowlist = map[string]struct{}{
	"User-Agent":      {},
	"Referer":         {},
	"Cookie":          {},
	"Authorization":   {},
	"Origin":          {},
	"Accept":          {},
	"Accept-Language": {},
}

const maxHeaderBytes = 32 * 1024

func (r AddRequest) headers() (http.Header, error) {
	h := make(http.Header)
	merge := func(k, v string) error {
		if v == "" {
			return nil
		}
		k = textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(k))
		if _, ok := headerAllowlist[k]; !ok {
			return nil
		}
		if strings.ContainsAny(v, "\r\n\x00") {
			return fmt.Errorf("请求头包含非法字符")
		}
		if h.Get(k) != "" {
			h.Set(k, h.Get(k)+"; "+v)
		} else {
			h.Set(k, v)
		}
		return nil
	}
	pairs := []struct{ k, v string }{
		{"Referer", r.Referer},
		{"User-Agent", r.UserAgent},
		{"Cookie", r.Cookie},
		{"Authorization", r.Authorization},
		{"Origin", r.Origin},
	}
	for _, p := range pairs {
		if err := merge(p.k, p.v); err != nil {
			return nil, err
		}
	}
	for k, v := range r.Headers {
		if err := merge(k, v); err != nil {
			return nil, err
		}
	}
	total := 0
	for k, vs := range h {
		for _, v := range vs {
			total += len(k) + len(v)
		}
	}
	if total > maxHeaderBytes {
		return nil, fmt.Errorf("请求头过大")
	}
	return h, nil
}

var trackingParams = map[string]struct{}{
	"fbclid":  {},
	"gclid":   {},
	"msclkid": {},
	"mc_cid":  {},
	"mc_eid":  {},
	"igshid":  {},
	"yclid":   {},
	"_ga":     {},
}

// normalizeURLForCompare strips fragments and common tracking parameters so
// duplicate detection does not depend on how the link was shared.
func normalizeURLForCompare(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") {
			q.Del(key)
			continue
		}
		if _, ok := trackingParams[lower]; ok {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// FindDuplicate reports an existing task for the same effective URL, directory
// and filename. A caller-specified different name or directory is treated as an
// intentional second copy and is not reported.
func (rt *Runtime) FindDuplicate(rawURL, dir, name string) *TaskView {
	u := strings.TrimSpace(rawURL)
	if !safeURL(u) || strings.HasPrefix(strings.ToLower(u), "magnet:") {
		return nil
	}
	norm := normalizeURLForCompare(u)
	wantedDir := strings.TrimSpace(dir)
	if wantedDir == "" {
		wantedDir = rt.Settings().DownloadDir
	} else if abs, err := filepath.Abs(wantedDir); err == nil {
		wantedDir = abs
	}
	wantedName := sanitizeName(name)
	var best *engine.Task
	for _, t := range rt.Mgr.List() {
		if t.Kind != engine.KindHTTP && t.Kind != engine.KindHLS && t.Kind != engine.KindDASH {
			continue
		}
		switch t.State {
		case engine.StateFailed, engine.StateCanceled:
			continue
		}
		if normalizeURLForCompare(t.URL) != norm && normalizeURLForCompare(t.FinalURL) != norm {
			continue
		}
		if !strings.EqualFold(filepath.Clean(t.Dir), filepath.Clean(wantedDir)) {
			continue
		}
		if wantedName != "" && t.Name != "" && !strings.EqualFold(wantedName, t.Name) {
			continue
		}
		if best == nil || t.CreatedAt.After(best.CreatedAt) {
			best = t
		}
	}
	if best == nil {
		return nil
	}
	v := toView(best)
	return &v
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == "" {
		return ""
	}
	return engine.SanitizeFilename(name)
}

// SetShowWindow registers how the shell brings the main window forward.
func (rt *Runtime) SetShowWindow(fn func()) {
	rt.showMu.Lock()
	rt.showFn = fn
	rt.showMu.Unlock()
}

func (rt *Runtime) showWindow() {
	rt.showMu.Lock()
	fn := rt.showFn
	rt.showMu.Unlock()
	if fn != nil {
		fn()
	}
}

// SetDirChooser registers the desktop folder picker used by fs.chooseDir.
func (rt *Runtime) SetDirChooser(fn func() (string, error)) {
	rt.chooseDirMu.Lock()
	rt.chooseDir = fn
	rt.chooseDirMu.Unlock()
}

func (rt *Runtime) rememberPick(dir string) {
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil || abs == "" {
		return
	}
	abs = filepath.Clean(abs)
	rt.pickMu.Lock()
	defer rt.pickMu.Unlock()
	if rt.picks == nil {
		rt.picks = make(map[string]time.Time)
	}
	now := time.Now()
	for k, exp := range rt.picks {
		if now.After(exp) {
			delete(rt.picks, k)
		}
	}
	rt.picks[abs] = now.Add(10 * time.Minute)
}

func (rt *Runtime) allowUntrustedDir(dir string) (string, bool) {
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil || abs == "" {
		return "", false
	}
	abs = filepath.Clean(abs)
	root := filepath.Clean(rt.Settings().DownloadDir)
	if strings.EqualFold(abs, root) {
		return abs, true
	}
	if rel, err := filepath.Rel(root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return abs, true
	}
	rt.pickMu.Lock()
	defer rt.pickMu.Unlock()
	exp, ok := rt.picks[abs]
	if !ok || time.Now().After(exp) {
		return "", false
	}
	return abs, true
}

// ChooseDir opens the desktop folder picker and remembers the result for a
// subsequent download.add from the extension.
func (rt *Runtime) ChooseDir() (string, error) {
	rt.chooseDirMu.Lock()
	fn := rt.chooseDir
	rt.chooseDirMu.Unlock()
	if fn == nil {
		return "", fmt.Errorf("请先打开 XDL 桌面端后再选择目录")
	}
	p, err := fn()
	if err != nil {
		return "", err
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("未选择目录")
	}
	rt.rememberPick(p)
	return p, nil
}

// Add creates a task. trusted=false is the native-host path: the extension may
// only set Dir to the default download tree or a folder freshly picked via fs.chooseDir.
func (rt *Runtime) Add(req AddRequest, trusted bool) (*TaskView, error) {
	u := strings.TrimSpace(req.URL)
	if !safeURL(u) {
		return nil, fmt.Errorf("只接受 http、https 或 magnet 链接")
	}
	if strings.HasPrefix(strings.ToLower(u), "magnet:") || req.Kind == string(engine.KindTorrent) {
		return rt.TorrentStart(req, trusted)
	}
	h, err := req.headers()
	if err != nil {
		return nil, err
	}
	dir := ""
	if trusted {
		dir = strings.TrimSpace(req.Dir)
		if dir != "" {
			if abs, err := filepath.Abs(dir); err == nil {
				dir = abs
			}
		}
	} else if d := strings.TrimSpace(req.Dir); d != "" {
		if abs, ok := rt.allowUntrustedDir(d); ok {
			dir = abs
		}
	}
	name := sanitizeName(req.Name)
	var category string
	dir, category = rt.applyCategory(u, name, dir)
	kind := engine.Kind(strings.ToLower(strings.TrimSpace(req.Kind)))
	var kindMeta json.RawMessage
	switch kind {
	case engine.KindHLS, engine.KindDASH:
		raw, err := job.EncodeMeta(job.Meta{
			Protocol:  string(kind),
			VariantID: strings.TrimSpace(req.VariantID),
			Label:     strings.TrimSpace(req.Label),
			MediaURL:  u,
		})
		if err != nil {
			return nil, err
		}
		kindMeta = raw
	case "":
		switch job.DetectKind(u) {
		case "hls":
			kind = engine.KindHLS
			raw, _ := job.EncodeMeta(job.Meta{Protocol: "hls", MediaURL: u})
			kindMeta = raw
		case "dash":
			kind = engine.KindDASH
			raw, _ := job.EncodeMeta(job.Meta{Protocol: "dash", MediaURL: u})
			kindMeta = raw
		default:
			kind = engine.KindHTTP
		}
	case engine.KindHTTP:
		// ok
	case engine.KindTorrent:
		return rt.TorrentStart(req, trusted)
	default:
		return nil, fmt.Errorf("不支持的任务类型：%s", kind)
	}
	t, err := rt.Mgr.Add(engine.AddParams{
		URL:      u,
		Dir:      dir,
		Name:     name,
		Headers:  h,
		Conns:    req.Conns,
		Kind:     kind,
		KindMeta: kindMeta,
		Category: category,
	})
	if err != nil {
		return nil, err
	}
	v := toView(t)
	return &v, nil
}

// MediaProbe fetches an HLS/DASH manifest and returns selectable variants.
func (rt *Runtime) MediaProbe(req AddRequest) (*job.ProbeResult, error) {
	u := strings.TrimSpace(req.URL)
	if !safeURL(u) || strings.HasPrefix(strings.ToLower(u), "magnet:") {
		return nil, fmt.Errorf("只接受 http(s) 播放列表链接")
	}
	h, err := req.headers()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return job.Probe(ctx, u, h)
}

// MediaStart creates an HLS/DASH task for the selected variant.
func (rt *Runtime) MediaStart(req AddRequest, trusted bool) (*TaskView, error) {
	u := strings.TrimSpace(req.URL)
	if !safeURL(u) || strings.HasPrefix(strings.ToLower(u), "magnet:") {
		return nil, fmt.Errorf("只接受 http(s) 播放列表链接")
	}
	kind := engine.Kind(strings.ToLower(strings.TrimSpace(req.Kind)))
	if kind == "" {
		kind = engine.Kind(job.DetectKind(u))
	}
	resolveHLSVariant := func() {
		pr, err := rt.MediaProbe(AddRequest{
			URL: req.URL, Referer: req.Referer, UserAgent: req.UserAgent,
			Cookie: req.Cookie, Authorization: req.Authorization, Origin: req.Origin, Headers: req.Headers,
		})
		if err != nil || len(pr.Variants) == 0 {
			return
		}
		if req.VariantID == "" && req.Label == "" {
			req.VariantID = pr.Variants[0].ID
			req.Label = pr.Variants[0].Label
		}
		for _, v := range pr.Variants {
			if (req.VariantID != "" && v.ID == req.VariantID) || (req.Label != "" && v.Label == req.Label) {
				req.URL = v.URI
				if req.Label == "" {
					req.Label = v.Label
				}
				return
			}
		}
	}
	if kind != engine.KindHLS && kind != engine.KindDASH {
		pr, err := rt.MediaProbe(req)
		if err != nil {
			return nil, err
		}
		kind = engine.Kind(pr.Kind)
		if kind == engine.KindHLS {
			resolveHLSVariant()
		} else if req.VariantID == "" && req.Label == "" && len(pr.Variants) > 0 {
			req.VariantID = pr.Variants[0].ID
			req.Label = pr.Variants[0].Label
		}
	} else if kind == engine.KindHLS && (req.VariantID != "" || req.Label != "" || job.DetectKind(u) != "") {
		// Master playlists need variant resolution; media playlists are fine as-is.
		resolveHLSVariant()
	}
	req.Kind = string(kind)
	if req.Name == "" && req.Label != "" {
		req.Name = sanitizeName("video [" + req.Label + "].mp4")
	}
	return rt.Add(req, trusted)
}

// TorrentProbe resolves magnet / .torrent metadata for the file-selection UI.
func (rt *Runtime) TorrentProbe(req AddRequest) (*bittorrent.ProbeResult, error) {
	s := rt.Settings()
	if !s.BTLegalAccepted && !req.AcceptLegal {
		return nil, fmt.Errorf("请先确认 BT 使用条款后再继续")
	}
	source := strings.TrimSpace(req.URL)
	if source == "" {
		source = strings.TrimSpace(req.TorrentPath)
	}
	if source == "" {
		return nil, fmt.Errorf("需要磁力链接或 .torrent 路径")
	}
	bt := rt.Mgr.BT()
	if bt == nil || bt.Client() == nil {
		return nil, fmt.Errorf("BT 客户端未启动")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return bt.Client().Probe(ctx, source)
}

// TorrentStart creates a torrent task after the user selected files.
func (rt *Runtime) TorrentStart(req AddRequest, trusted bool) (*TaskView, error) {
	s := rt.Settings()
	if req.AcceptLegal && !s.BTLegalAccepted {
		s.BTLegalAccepted = true
		if _, err := rt.SaveSettings(s); err != nil {
			return nil, err
		}
		s = rt.Settings()
	}
	if !s.BTLegalAccepted {
		return nil, fmt.Errorf("请先确认 BT 使用条款后再继续")
	}
	if !trusted {
		return nil, fmt.Errorf("浏览器扩展暂不支持添加 BT 任务，请在 XDL 中打开")
	}

	source := strings.TrimSpace(req.URL)
	torrentPath := strings.TrimSpace(req.TorrentPath)
	if source == "" {
		source = torrentPath
	}
	if source == "" {
		return nil, fmt.Errorf("需要磁力链接或 .torrent 路径")
	}

	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir = s.DownloadDir
	} else if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	infoHash := strings.TrimSpace(req.InfoHash)
	name := sanitizeName(req.Name)
	selected := append([]int(nil), req.Selected...)
	magnet := ""
	if bittorrent.IsMagnet(source) {
		magnet = source
	} else if bittorrent.IsTorrentPath(source) {
		torrentPath = source
	} else if torrentPath == "" {
		return nil, fmt.Errorf("只接受 magnet: 链接或 .torrent 文件")
	}

	// Resolve metadata when the UI skipped probe (e.g. second-instance magnet).
	if infoHash == "" || name == "" {
		pr, err := rt.TorrentProbe(AddRequest{URL: source, TorrentPath: torrentPath, AcceptLegal: true})
		if err != nil {
			return nil, err
		}
		if infoHash == "" {
			infoHash = pr.InfoHash
		}
		if name == "" {
			name = sanitizeName(pr.Name)
		}
		if len(selected) == 0 {
			selected = make([]int, len(pr.Files))
			for i := range pr.Files {
				selected[i] = i
			}
		}
	}

	seedAfter := s.BTSeedAfterDone
	if req.SeedAfter != nil {
		seedAfter = *req.SeedAfter
	}
	raw, err := bittorrent.EncodeMeta(bittorrent.Meta{
		InfoHash:    infoHash,
		Magnet:      magnet,
		TorrentPath: torrentPath,
		Name:        name,
		Selected:    selected,
		SeedAfter:   seedAfter,
		DataDir:     dir,
	})
	if err != nil {
		return nil, err
	}
	url := magnet
	if url == "" {
		url = torrentPath
	}
	t, err := rt.Mgr.Add(engine.AddParams{
		URL:      url,
		Dir:      dir,
		Name:     name,
		Kind:     engine.KindTorrent,
		KindMeta: raw,
	})
	if err != nil {
		return nil, err
	}
	v := toView(t)
	return &v, nil
}

// AcceptBTLegal records the first-use legal acknowledgement.
func (rt *Runtime) AcceptBTLegal() (settings.Settings, error) {
	s := rt.Settings()
	s.BTLegalAccepted = true
	return rt.SaveSettings(s)
}

// IntegrationStatus merges native-host registration with last-seen client data.
func (rt *Runtime) IntegrationStatus() map[string]any {
	st := nativehost.QueryStatus(NativeDir())
	rt.seenMu.Lock()
	seen := rt.seen
	rt.seenMu.Unlock()
	online := !seen.LastSeen.IsZero() && time.Since(seen.LastSeen) < 3*time.Minute
	return map[string]any{
		"host":          st,
		"extensionId":   nativehost.DevExtensionID,
		"extensionSeen": seen,
		"extensionLive": online,
	}
}

func (rt *Runtime) markSeen(info clientInfo) {
	if info.Version == "" && info.Browser == "" && info.ID == "" {
		return
	}
	rt.seenMu.Lock()
	rt.seen = ExtensionSeen{
		LastSeen: time.Now(),
		Version:  info.Version,
		Browser:  info.Browser,
		ID:       info.ID,
	}
	rt.seenMu.Unlock()
}

type clientInfo struct {
	Version string `json:"version"`
	Browser string `json:"browser"`
	ID      string `json:"id"`
}

type dedupeEntry struct {
	At   time.Time
	View TaskView
}

func (rt *Runtime) dedupeGet(clientRequestID string) (TaskView, bool) {
	if clientRequestID == "" {
		return TaskView{}, false
	}
	rt.dedupeMu.Lock()
	defer rt.dedupeMu.Unlock()
	e, ok := rt.dedupe[clientRequestID]
	if !ok || time.Since(e.At) > 5*time.Minute {
		delete(rt.dedupe, clientRequestID)
		return TaskView{}, false
	}
	return e.View, true
}

func (rt *Runtime) dedupePut(clientRequestID string, v TaskView) {
	if clientRequestID == "" {
		return
	}
	rt.dedupeMu.Lock()
	defer rt.dedupeMu.Unlock()
	if len(rt.dedupe) > 256 {
		for k, e := range rt.dedupe {
			if time.Since(e.At) > 5*time.Minute {
				delete(rt.dedupe, k)
			}
		}
	}
	rt.dedupe[clientRequestID] = dedupeEntry{At: time.Now(), View: v}
}

func (rt *Runtime) handle(_ context.Context, req ipc.Request) ipc.Response {
	switch req.Method {
	case ipc.MethodPing:
		var p struct {
			Client clientInfo `json:"client"`
		}
		_ = json.Unmarshal(req.Params, &p)
		rt.markSeen(p.Client)
		return ipc.OK(req.ID, map[string]any{
			"status":       "ok",
			"name":         "XDL",
			"version":      Version,
			"downloadDir":  rt.Settings().DownloadDir,
			"integration":  rt.IntegrationStatus(),
			"capabilities": []string{"http", "downloads", "context-menu", "media-sniff", "hls", "dash", "batch", "grab", "choose-dir"},
		})
	case ipc.MethodFSChooseDir:
		p, err := rt.ChooseDir()
		if err != nil {
			return ipc.Fail(req.ID, ipc.ErrInternal, err.Error(), map[string]string{"code": "CHOOSE_DIR"})
		}
		return ipc.OK(req.ID, map[string]string{"dir": p})
	case ipc.MethodStatus:
		tasks, active, speed := rt.Mgr.Stats()
		return ipc.OK(req.ID, map[string]any{
			"ready":   true,
			"tasks":   tasks,
			"running": active,
			"speed":   speed,
		})
	case ipc.MethodDownloadAdd:
		var p AddRequest
		if err := json.Unmarshal(req.Params, &p); err != nil || p.URL == "" {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, "url required", nil)
		}
		if prev, ok := rt.dedupeGet(req.ClientRequestID); ok {
			return ipc.OK(req.ID, prev)
		}
		v, err := rt.Add(p, false)
		if err != nil {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, err.Error(), nil)
		}
		rt.dedupePut(req.ClientRequestID, *v)
		rt.showWindow()
		return ipc.OK(req.ID, v)
	case ipc.MethodDownloadList:
		return ipc.OK(req.ID, rt.ListViews())
	case ipc.MethodDownloadPause:
		id, ok := paramID(req)
		if !ok {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, "id required", nil)
		}
		if err := rt.Mgr.Pause(engine.TaskID(id)); err != nil {
			return ipc.Fail(req.ID, ipc.ErrInternal, err.Error(), nil)
		}
		return ipc.OK(req.ID, map[string]string{"status": "paused"})
	case ipc.MethodDownloadResume:
		id, ok := paramID(req)
		if !ok {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, "id required", nil)
		}
		if err := rt.Mgr.Resume(engine.TaskID(id)); err != nil {
			return ipc.Fail(req.ID, ipc.ErrInternal, err.Error(), nil)
		}
		return ipc.OK(req.ID, map[string]string{"status": "resumed"})
	case ipc.MethodDownloadRemove:
		id, ok := paramID(req)
		if !ok {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, "id required", nil)
		}
		if err := rt.Mgr.Remove(engine.TaskID(id)); err != nil {
			return ipc.Fail(req.ID, ipc.ErrInternal, err.Error(), nil)
		}
		return ipc.OK(req.ID, map[string]string{"status": "removed"})
	case ipc.MethodSettingsGet:
		return ipc.OK(req.ID, rt.Settings())
	case ipc.MethodSettingsSet:
		return ipc.Fail(req.ID, ipc.ErrForbidden, "请在 XDL 桌面端设置页修改", nil)
	case ipc.MethodIntegrationRepair:
		if err := RepairNativeHost(); err != nil {
			return ipc.Fail(req.ID, ipc.ErrInternal, err.Error(), nil)
		}
		return ipc.OK(req.ID, rt.IntegrationStatus())
	case ipc.MethodIntegrationStatus:
		return ipc.OK(req.ID, rt.IntegrationStatus())
	case ipc.MethodMediaProbe:
		var p AddRequest
		if err := json.Unmarshal(req.Params, &p); err != nil || p.URL == "" {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, "url required", nil)
		}
		out, err := rt.MediaProbe(p)
		if err != nil {
			return ipc.Fail(req.ID, ipc.ErrInternal, err.Error(), nil)
		}
		return ipc.OK(req.ID, out)
	case ipc.MethodMediaStart:
		var p AddRequest
		if err := json.Unmarshal(req.Params, &p); err != nil || p.URL == "" {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, "url required", nil)
		}
		if prev, ok := rt.dedupeGet(req.ClientRequestID); ok {
			return ipc.OK(req.ID, prev)
		}
		v, err := rt.MediaStart(p, false)
		if err != nil {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, err.Error(), nil)
		}
		rt.dedupePut(req.ClientRequestID, *v)
		rt.showWindow()
		return ipc.OK(req.ID, v)
	case ipc.MethodTorrentProbe, ipc.MethodTorrentStart:
		return ipc.Fail(req.ID, ipc.ErrForbidden, "请在 XDL 桌面端添加 BT / 磁力任务", nil)
	case ipc.MethodGrabProbe:
		var p GrabRequest
		if err := json.Unmarshal(req.Params, &p); err != nil || p.URL == "" {
			return ipc.Fail(req.ID, ipc.ErrInvalidParams, "url required", nil)
		}
		out, err := rt.GrabProbe(p)
		if err != nil {
			return ipc.Fail(req.ID, ipc.ErrInternal, err.Error(), nil)
		}
		return ipc.OK(req.ID, out)
	case ipc.MethodGrabEnqueue, ipc.MethodBatchAdd:
		return ipc.Fail(req.ID, ipc.ErrForbidden, "请在 XDL 桌面端确认后入队", nil)
	default:
		return ipc.Fail(req.ID, ipc.ErrNoMethod, "unknown method: "+req.Method, nil)
	}
}

func safeURL(u string) bool {
	return len(u) >= 7 && (hasPrefixFold(u, "http://") || hasPrefixFold(u, "https://") || hasPrefixFold(u, "magnet:"))
}

func hasPrefixFold(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		a, b := s[i], p[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

func paramID(req ipc.Request) (string, bool) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ID == "" {
		return "", false
	}
	return p.ID, true
}
