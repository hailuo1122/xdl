package app

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"github.com/xdl-app/xdl/internal/bittorrent"
	"github.com/xdl-app/xdl/internal/engine"
	"github.com/xdl-app/xdl/internal/grab"
	"github.com/xdl-app/xdl/internal/media/job"
	"github.com/xdl-app/xdl/internal/nativehost"
	"github.com/xdl-app/xdl/internal/settings"
	"github.com/xdl-app/xdl/internal/updater"
)

//go:embed all:assets
var assets embed.FS

//go:embed appicon/icon.png
var appIcon []byte

var instanceKey = [32]byte{
	0x58, 0x44, 0x4c, 0x2e, 0x76, 0x31, 0x00, 0x11,
	0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99,
	0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x10, 0x20,
	0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0,
}

// Service is the Wails-bound API used by the React shell.
type Service struct {
	rt  *Runtime
	app *application.App
	win *application.WebviewWindow
	ns  *notifications.NotificationService
}

// TaskDetail adds the chunk snapshot used by the connection inspector.
type TaskDetail struct {
	Task        TaskView       `json:"task"`
	Chunks      []engine.Chunk `json:"chunks"`
	Connections int            `json:"connections"`
}

func (s *Service) Status() map[string]any {
	tasks, active, speed := s.rt.Mgr.Stats()
	cfg := s.rt.Settings()
	return map[string]any{
		"version":     Version,
		"ready":       true,
		"tasks":       tasks,
		"running":     active,
		"speed":       speed,
		"limit":       cfg.GlobalLimitBps,
		"downloadDir": cfg.DownloadDir,
		"settings":    cfg,
		"integration": s.rt.IntegrationStatus(),
		"connected":   true,
	}
}

func (s *Service) List() []TaskView {
	return s.rt.ListViews()
}

func (s *Service) Detail(id string) (*TaskDetail, error) {
	t, ok := s.rt.Mgr.Get(engine.TaskID(id))
	if !ok {
		return nil, fmt.Errorf("任务不存在")
	}
	chunks := s.rt.Mgr.Chunks(engine.TaskID(id))
	owners := make(map[int]struct{})
	for _, c := range chunks {
		if c.State == engine.ChunkRunning {
			owners[c.Owner] = struct{}{}
		}
	}
	return &TaskDetail{
		Task:        toView(t),
		Chunks:      chunks,
		Connections: len(owners),
	}, nil
}

func (s *Service) Add(url string) (string, error) {
	v, err := s.rt.Add(AddRequest{URL: url}, true)
	if err != nil {
		return "", err
	}
	return v.ID, nil
}

func (s *Service) AddAdvanced(req AddRequest) (*TaskView, error) {
	return s.rt.Add(req, true)
}

func (s *Service) MediaProbe(req AddRequest) (*job.ProbeResult, error) {
	return s.rt.MediaProbe(req)
}

func (s *Service) MediaStart(req AddRequest) (*TaskView, error) {
	return s.rt.MediaStart(req, true)
}

func (s *Service) TorrentProbe(req AddRequest) (*bittorrent.ProbeResult, error) {
	return s.rt.TorrentProbe(req)
}

func (s *Service) TorrentStart(req AddRequest) (*TaskView, error) {
	return s.rt.TorrentStart(req, true)
}

func (s *Service) GrabProbe(req GrabRequest) (*grab.Result, error) {
	return s.rt.GrabProbe(req)
}

func (s *Service) GrabEnqueue(req GrabEnqueueRequest) (*BatchAddResult, error) {
	return s.rt.GrabEnqueue(req)
}

func (s *Service) BatchAdd(req BatchAddRequest) (*BatchAddResult, error) {
	return s.rt.BatchAdd(req)
}

func (s *Service) CheckUpdate() updater.CheckResult {
	return s.rt.CheckUpdate()
}

func (s *Service) AcceptBTLegal() (settings.Settings, error) {
	return s.rt.AcceptBTLegal()
}

func (s *Service) ChooseTorrentFile() (string, error) {
	p, err := s.app.Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		SetTitle("选择种子文件").
		AddFilter("Torrent", "*.torrent").
		AttachToWindow(s.win).
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return p, nil
}

func (s *Service) ChooseURLListFile() (string, error) {
	p, err := s.app.Dialog.OpenFile().
		CanChooseFiles(true).
		CanChooseDirectories(false).
		SetTitle("导入 URL 列表").
		AddFilter("文本", "*.txt;*.csv").
		AttachToWindow(s.win).
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return p, nil
}

func (s *Service) ReadTextFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("未选择文件")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) > 2<<20 {
		return "", fmt.Errorf("文件过大（上限 2MB）")
	}
	return string(raw), nil
}

// FindDuplicate powers the "already exists / keep both" prompt in the new-task
// dialog. It never blocks adding; the UI decides whether to reuse or proceed.
func (s *Service) FindDuplicate(url, dir, name string) *TaskView {
	return s.rt.FindDuplicate(url, dir, name)
}

func (s *Service) Pause(id string) error {
	return s.rt.Mgr.Pause(engine.TaskID(id))
}

func (s *Service) Resume(id string) error {
	return s.rt.Mgr.Resume(engine.TaskID(id))
}

func (s *Service) SetPriority(id string, priority int) error {
	return s.rt.Mgr.SetPriority(engine.TaskID(id), priority)
}

func (s *Service) SetLimitBps(id string, bps int64) error {
	return s.rt.Mgr.SetLimitBps(engine.TaskID(id), bps)
}

func (s *Service) Remove(id string) error {
	return s.rt.Mgr.Remove(engine.TaskID(id))
}

func (s *Service) PauseAll() int {
	return s.rt.Mgr.PauseAll()
}

func (s *Service) ResumeAll() int {
	return s.rt.Mgr.ResumeAll()
}

func (s *Service) OpenFile(id string) error {
	t, ok := s.rt.Mgr.Get(engine.TaskID(id))
	if !ok {
		return fmt.Errorf("任务不存在")
	}
	if t.State != engine.StateDone || t.FinalPath == "" {
		return fmt.Errorf("文件尚未下载完成")
	}
	if _, err := os.Stat(t.FinalPath); err != nil {
		return fmt.Errorf("文件已不存在，可能被移动或删除")
	}
	return s.app.Browser.OpenFile(t.FinalPath)
}

func (s *Service) RevealFile(id string) error {
	t, ok := s.rt.Mgr.Get(engine.TaskID(id))
	if !ok {
		return fmt.Errorf("任务不存在")
	}
	p := t.FinalPath
	if p == "" {
		p = t.Dir
	}
	if _, err := os.Stat(p); err != nil && t.Dir != "" {
		p = t.Dir
	}
	if err := revealInExplorer(p); err != nil {
		return err
	}
	return nil
}

func revealInExplorer(p string) error {
	switch goruntime.GOOS {
	case "windows":
		cmd := exec.Command("explorer.exe", "/select,"+p)
		return cmd.Start()
	case "darwin":
		return exec.Command("open", "-R", p).Start()
	default:
		return exec.Command("xdg-open", p).Start()
	}
}

func (s *Service) ChooseDirectory() (string, error) {
	if s.win != nil {
		s.win.Show()
		s.win.Focus()
	}
	dir := s.rt.Settings().DownloadDir
	dlg := s.app.Dialog.OpenFile().
		CanChooseFiles(false).
		CanChooseDirectories(true).
		SetTitle("选择下载目录").
		SetDirectory(dir)
	if s.win != nil {
		dlg = dlg.AttachToWindow(s.win)
	}
	p, err := dlg.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return p, nil
}

func (s *Service) GetSettings() settings.Settings {
	return s.rt.Settings()
}

func (s *Service) SaveSettings(in settings.Settings) (settings.Settings, error) {
	old := s.rt.Settings()
	out, err := s.rt.SaveSettings(in)
	if err != nil {
		return old, err
	}
	if old.LaunchAtLogin != out.LaunchAtLogin {
		if err := applyAutostart(s.app, out.LaunchAtLogin); err != nil {
			slog.Warn("autostart", "err", err)
		}
	}
	s.app.Event.Emit("settings.changed", out)
	return out, nil
}

func (s *Service) Integration() map[string]any {
	return s.rt.IntegrationStatus()
}

func (s *Service) RepairIntegration() (map[string]any, error) {
	if err := RepairNativeHost(); err != nil {
		return s.rt.IntegrationStatus(), err
	}
	return s.rt.IntegrationStatus(), nil
}

func (s *Service) OpenURL(url string) error {
	u := strings.TrimSpace(url)
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return fmt.Errorf("不支持的链接")
	}
	return s.app.Browser.OpenURL(u)
}

// ExtensionPath returns the first packaged/development extension directory
// present on disk. Empty means the extension has not been built/copied.
func (s *Service) ExtensionPath() string {
	return findExtensionDir()
}

func (s *Service) OpenExtensionFolder() error {
	p := findExtensionDir()
	if p == "" {
		return fmt.Errorf("还没有找到扩展目录，请先运行 scripts/build-alpha.ps1")
	}
	return openFolder(p)
}

// Diagnostics returns a copyable, secret-free snapshot for alpha bug reports.
func (s *Service) Diagnostics() string {
	cfg := s.rt.Settings()
	integration := s.rt.IntegrationStatus()
	raw, _ := json.MarshalIndent(integration, "", "  ")
	extPath := findExtensionDir()
	if extPath == "" {
		extPath = "-"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "XDL 诊断信息\n")
	fmt.Fprintf(&b, "appVersion: %s\n", Version)
	fmt.Fprintf(&b, "dataDir: %s\n", DataDir())
	fmt.Fprintf(&b, "downloadDir: %s\n", cfg.DownloadDir)
	fmt.Fprintf(&b, "logPath: %s\n", filepath.Join(DataDir(), "logs", "xdl.log"))
	fmt.Fprintf(&b, "extensionPath: %s\n", extPath)
	fmt.Fprintf(&b, "settings: notifications=%t launchAtLogin=%t startMinimized=%t closeToTray=%t maxConcurrent=%d defaultConns=%d turbo=%t limitBps=%d\n",
		cfg.Notifications, cfg.LaunchAtLogin, cfg.StartMinimized, cfg.CloseToTray,
		cfg.MaxConcurrent, cfg.DefaultConns, cfg.Turbo, cfg.GlobalLimitBps)
	fmt.Fprintf(&b, "tasks: %d\n", len(s.rt.ListViews()))
	fmt.Fprintf(&b, "integration: %s\n", string(raw))
	return b.String()
}

func (s *Service) OpenLogFolder() error {
	dir := filepath.Join(DataDir(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return openFolder(dir)
}

func openFolder(p string) error {
	switch goruntime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", p).Start()
	case "darwin":
		return exec.Command("open", p).Start()
	default:
		return exec.Command("xdg-open", p).Start()
	}
}

// findExtensionDir prefers the deployed copy under the data directory so the
// path shown in settings stays stable across builds and working directories.
func findExtensionDir() string {
	var candidates []string
	candidates = append(candidates, filepath.Join(DataDir(), "extension"))
	if env := strings.TrimSpace(os.Getenv("XDL_EXTENSION_PATH")); env != "" {
		candidates = append(candidates, env)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "extension"),
			filepath.Join(dir, "extension", ".output", "chrome-mv3"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "extension", ".output", "chrome-mv3"),
			filepath.Join(wd, "extension"),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(c, "manifest.json")); err == nil {
				return c
			}
		}
	}
	return ""
}

func (s *Service) Quit() {
	s.app.Quit()
}

func applyAutostart(app *application.App, enabled bool) error {
	if enabled {
		return app.Autostart.EnableWithOptions(application.AutostartOptions{
			Arguments: []string{"--minimized"},
		})
	}
	return app.Autostart.Disable()
}

type trayState struct {
	tray        *application.SystemTray
	showItem    *application.MenuItem
	pauseItem   *application.MenuItem
	resumeItem  *application.MenuItem
	integration *application.MenuItem
	limitItems  map[int64]*application.MenuItem
	lastSeen    time.Time
}

// RunGUI starts the desktop shell. Closing the window hides it; Quit exits.
func RunGUI(minimized bool) int {
	InitLogging()
	rt, err := Open()
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			slog.Info("another instance is already running")
			return 0
		}
		slog.Error("open runtime", "err", err)
		return 1
	}
	ctx, stop := signalContext()
	defer stop()
	rt.Serve(ctx)
	rt.restartAria2()
	defer rt.Close()

	if path, err := DeployExtension(); err != nil {
		slog.Warn("deploy extension", "err", err)
	} else {
		slog.Info("extension ready", "path", path)
	}
	go func() {
		if err := EnsureNativeHostCurrent(); err != nil {
			slog.Warn("refresh native host", "err", err)
		}
	}()

	ns := notifications.New()
	svc := &Service{rt: rt, ns: ns}
	rt.SetDirChooser(svc.ChooseDirectory)

	winOptions := application.WindowsOptions{
		DisableQuitOnLastWindowClosed: true,
	}
	if port := strings.TrimSpace(os.Getenv("XDL_DEBUG_PORT")); port != "" {
		winOptions.AdditionalBrowserArgs = []string{"--remote-debugging-port=" + port}
	}

	var window *application.WebviewWindow
	var quitting atomic.Bool

	var app *application.App
	app = application.New(application.Options{
		Name:        "XDL",
		Description: "XDL 高速下载器",
		Icon:        appIcon,
		LogLevel:    slog.LevelInfo,
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(ns),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Windows: winOptions,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:      "com.xdl.app",
			EncryptionKey: instanceKey,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if window != nil {
					window.Show()
					window.Focus()
				}
				for _, arg := range data.Args {
					low := strings.ToLower(arg)
					if strings.HasPrefix(low, "magnet:") || strings.HasSuffix(low, ".torrent") {
						app.Event.Emit("ui.open-new", map[string]string{"prefill": arg})
						continue
					}
					if safeURL(arg) {
						v, err := rt.Add(AddRequest{URL: arg}, true)
						if err != nil {
							app.Event.Emit("ui.toast", map[string]string{"kind": "error", "text": err.Error()})
						} else {
							app.Event.Emit("external.added", v)
						}
					}
				}
			},
		},
		OnShutdown: func() {
			quitting.Store(true)
			stop()
		},
	})
	svc.app = app
	svc.win = nil

	cfg := rt.Settings()
	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "XDL",
		Width:            1220,
		Height:           800,
		MinWidth:         980,
		MinHeight:        620,
		URL:              "/",
		Hidden:           minimized || cfg.StartMinimized,
		BackgroundColour: application.RGBA{Red: 17, Green: 17, Blue: 19, Alpha: 255},
	})
	svc.win = window
	rt.SetShowWindow(func() {
		application.InvokeSync(func() {
			window.Show()
			window.Focus()
		})
	})

	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		if rt.Settings().CloseToTray {
			window.Hide()
			e.Cancel()
			return
		}
		quitting.Store(true)
		app.Quit()
		e.Cancel()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(appIcon)
	tray.SetTooltip("XDL")
	menu := app.NewMenu()
	ts := &trayState{tray: tray, limitItems: map[int64]*application.MenuItem{}}

	ts.showItem = menu.Add("显示 XDL").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
	})
	menu.AddSeparator()
	ts.pauseItem = menu.Add("全部暂停").OnClick(func(*application.Context) {
		n := svc.PauseAll()
		slog.Info("pause all", "count", n)
	})
	ts.resumeItem = menu.Add("全部继续").OnClick(func(*application.Context) {
		n := svc.ResumeAll()
		slog.Info("resume all", "count", n)
	})
	limitMenu := menu.AddSubmenu("限速")
	limits := []struct {
		bps   int64
		label string
	}{
		{0, "不限速"},
		{1 << 20, "1 MB/s"},
		{3 << 20, "3 MB/s"},
		{5 << 20, "5 MB/s"},
		{10 << 20, "10 MB/s"},
	}
	for _, l := range limits {
		bps := l.bps
		item := limitMenu.AddRadio(l.label, cfg.GlobalLimitBps == bps)
		item.OnClick(func(*application.Context) {
			cur := rt.Settings()
			cur.GlobalLimitBps = bps
			if _, err := rt.SaveSettings(cur); err != nil {
				slog.Warn("save limit", "err", err)
			}
			app.Event.Emit("settings.changed", rt.Settings())
			updateLimitChecks(ts, bps)
		})
		ts.limitItems[bps] = item
	}
	menu.AddSeparator()
	ts.integration = menu.Add("浏览器集成：检测中…").OnClick(func(*application.Context) {
		window.Show()
		window.Focus()
		app.Event.Emit("ui.open-settings", map[string]string{"section": "browser"})
	})
	menu.AddSeparator()
	menu.Add("退出 XDL").OnClick(func(*application.Context) {
		quitting.Store(true)
		app.Quit()
	})
	tray.SetMenu(menu)
	tray.OnClick(func() {
		window.Show()
		window.Focus()
	})

	if cfg.LaunchAtLogin {
		go func() {
			time.Sleep(800 * time.Millisecond)
			if err := applyAutostart(app, true); err != nil {
				slog.Warn("autostart", "err", err)
			}
		}()
	}

	go emitEngineEvents(app, rt, ctx)
	go sendNotifications(rt, ns, ctx)
	go updateTray(ts, rt, ctx)

	app.Event.Emit("app.ready", map[string]string{"version": Version})
	if err := app.Run(); err != nil {
		slog.Error("wails", "err", err)
		return 1
	}
	return 0
}

func updateLimitChecks(ts *trayState, bps int64) {
	for v, item := range ts.limitItems {
		item.SetChecked(v == bps)
	}
}

func emitEngineEvents(app *application.App, rt *Runtime, ctx context.Context) {
	ch := rt.Mgr.Bus().Subscribe(256)
	defer rt.Mgr.Bus().Unsubscribe(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch ev.Type {
			case engine.EventTaskUpdated:
				if t, ok := ev.Data.(*engine.Task); ok {
					app.Event.Emit("task.updated", toView(t))
				}
			case engine.EventTaskProgress:
				app.Event.Emit("task.progress", ev.Data)
			case engine.EventTaskRemoved:
				app.Event.Emit("task.removed", ev.Data)
			}
		}
	}
}

func sendNotifications(rt *Runtime, ns *notifications.NotificationService, ctx context.Context) {
	ch := rt.Mgr.Bus().Subscribe(128)
	defer rt.Mgr.Bus().Unsubscribe(ch)
	prev := make(map[engine.TaskID]engine.State)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Type != engine.EventTaskUpdated {
				continue
			}
			t, ok := ev.Data.(*engine.Task)
			if !ok {
				continue
			}
			before := prev[t.ID]
			prev[t.ID] = t.State
			if before == t.State || !rt.Settings().Notifications {
				continue
			}
			switch t.State {
			case engine.StateDone:
				playAlert("SystemAsterisk")
				_ = ns.SendNotification(notifications.NotificationOptions{
					ID:    "xdl-done-" + string(t.ID),
					Title: "下载完成",
					Body:  t.Name,
				})
			case engine.StateFailed:
				playAlert("SystemExclamation")
				body := t.Name
				if t.Err != nil {
					body += "\n" + t.Err.Message
					if t.Err.Hint != "" {
						body += " · " + t.Err.Hint
					}
				}
				_ = ns.SendNotification(notifications.NotificationOptions{
					ID:    "xdl-fail-" + string(t.ID),
					Title: "下载失败",
					Body:  body,
				})
			}
		}
	}
}

func updateTray(ts *trayState, rt *Runtime, ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastIntegration := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tasks, active, speed := rt.Mgr.Stats()
			application.InvokeSync(func() {
				ts.tray.SetTooltip(fmt.Sprintf("XDL · %s/s · %d 个任务", humanBytes(speed), tasks))
				ts.pauseItem.SetEnabled(active > 0)
			})
			if time.Since(lastIntegration) > 5*time.Second {
				lastIntegration = time.Now()
				st := nativehost.QueryStatus(NativeDir())
				label := "浏览器集成：未安装（点击设置）"
				if st.Installed {
					label = "浏览器集成：已就绪"
				}
				bps := rt.Settings().GlobalLimitBps
				application.InvokeSync(func() {
					ts.integration.SetLabel(label)
					updateLimitChecks(ts, bps)
				})
			}
		}
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// RunHeadless keeps the engine and pipe without a window (automation / debug).
func RunHeadless() int {
	InitLogging()
	rt, err := Open()
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			slog.Info("another instance is already running")
			return 0
		}
		slog.Error("open runtime", "err", err)
		return 1
	}
	defer rt.Close()
	ctx, stop := signalContext()
	defer stop()
	rt.Serve(ctx)
	rt.restartAria2()
	os.Stdout.WriteString("XDL headless (pipe \\\\.\\pipe\\xdl.v1)\n")
	<-ctx.Done()
	return 0
}
