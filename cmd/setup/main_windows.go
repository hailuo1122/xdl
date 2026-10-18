//go:build windows

// XDL setup is a per-user installer with its own window. It does not use NSIS.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// Version is stamped by scripts/pack-setup.ps1.
var Version = "0.0.5-m5"

func main() {
	mode := "install"
	preview := false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--uninstall":
			mode = "uninstall"
		case "--preview":
			preview = true
		case "--help", "-h":
			fmt.Println("XDL setup")
			fmt.Println("  (no args)     install")
			fmt.Println("  --uninstall   remove this install")
			fmt.Println("  --preview     show the window with a simulated install")
			return
		}
	}
	if strings.EqualFold(filepath.Base(os.Args[0]), "uninstall.exe") {
		mode = "uninstall"
	}

	enablePerMonitorDPI()

	title := "安装 XDL"
	if mode == "uninstall" {
		title = "卸载 XDL"
	}
	dataDir := filepath.Join(os.TempDir(), "XDL-setup-ui")
	_ = os.MkdirAll(dataDir, 0o755)

	width, height := scaleDIP(800), scaleDIP(520)
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		DataPath:  dataDir,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			Center: true,
		},
	})
	if w == nil {
		messageBox(title, "无法打开安装窗口。请安装 Microsoft Edge WebView2 运行时后再试。Windows 11 通常已经自带。")
		os.Exit(1)
	}
	defer w.Destroy()
	w.SetSize(width, height, webview2.HintFixed)

	bridge := &Bridge{w: w, mode: mode, preview: preview}
	binds := []struct {
		name string
		fn   any
	}{
		{"setupInfo", bridge.Info},
		{"setupBrowse", bridge.Browse},
		{"setupInstall", bridge.Install},
		{"setupUninstall", bridge.Uninstall},
		{"setupLaunch", bridge.Launch},
		{"setupClose", bridge.Close},
	}
	for _, b := range binds {
		if err := w.Bind(b.name, b.fn); err != nil {
			messageBox(title, "安装窗口初始化失败："+err.Error())
			os.Exit(1)
		}
	}
	w.SetHtml(uiHTML)
	w.Run()
}

var (
	procSetProcessDpiAwarenessContext = windows.NewLazySystemDLL("user32.dll").NewProc("SetProcessDpiAwarenessContext")
	procGetDpiForSystem               = windows.NewLazySystemDLL("user32.dll").NewProc("GetDpiForSystem")
)

func enablePerMonitorDPI() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 is -4.
	_, _, _ = procSetProcessDpiAwarenessContext.Call(^uintptr(3))
}

func scaleDIP(px int) int {
	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi == 0 {
		dpi = 96
	}
	return int(int64(px) * int64(dpi) / 96)
}

func messageBox(title, text string) {
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	b, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	_, _ = windows.MessageBox(0, b, t, windows.MB_OK|windows.MB_ICONERROR)
}

// Bridge is the function set bound into the installer page.
type Bridge struct {
	w       webview2.WebView
	mode    string
	preview bool
}

func (b *Bridge) Info() (Info, error) {
	dir := defaultInstallDir()
	installed := ""
	if ver, loc, err := readInstallRecord(); err == nil {
		installed = ver
		if b.mode == "install" && loc != "" {
			dir = loc
		}
	}
	if b.mode == "uninstall" {
		if here := currentExeDir(); strings.EqualFold(filepath.Base(os.Args[0]), "uninstall.exe") && here != "" {
			dir = here
		} else if _, loc, err := readInstallRecord(); err == nil && loc != "" {
			dir = loc
		}
	}
	return Info{
		Version:    Version,
		Dir:        dir,
		Mode:       b.mode,
		Installed:  installed,
		HasPayload: b.preview || hasPayload(),
	}, nil
}

func (b *Bridge) Browse(initial string) (string, error) {
	hwnd := windows.HWND(uintptr(unsafe.Pointer(b.w.Window())))
	return browseFolder(hwnd, initial)
}

func (b *Bridge) Install(dir string) error {
	report := b.reporter()
	if b.preview {
		return runPreviewInstall(report)
	}
	return install(dir, report)
}

func (b *Bridge) Uninstall() error {
	dir := currentExeDir()
	if !strings.EqualFold(filepath.Base(os.Args[0]), "uninstall.exe") {
		if _, loc, err := readInstallRecord(); err == nil && loc != "" {
			dir = loc
		}
	}
	if b.preview {
		return runPreviewInstall(b.reporter())
	}
	return uninstall(dir, b.reporter())
}

func (b *Bridge) Launch() error {
	if b.preview {
		return nil
	}
	dir := defaultInstallDir()
	if _, loc, err := readInstallRecord(); err == nil && loc != "" {
		dir = loc
	}
	return launchApp(dir)
}

func (b *Bridge) Close() error {
	b.w.Terminate()
	return nil
}

func (b *Bridge) reporter() func(Progress) {
	return func(p Progress) {
		msg := p.JSON()
		b.w.Dispatch(func() {
			b.w.Eval("window.onProgress(" + msg + ")")
		})
	}
}
