//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\XDL`

var errBadPayload = errors.New("安装包内容异常，已停止。")

// Info is the snapshot the window reads on open.
type Info struct {
	Version    string `json:"version"`
	Dir        string `json:"dir"`
	Mode       string `json:"mode"`
	Installed  string `json:"installed"`
	HasPayload bool   `json:"hasPayload"`
}

// Progress is one beat of the transfer lanes.
type Progress struct {
	Phase  string  `json:"phase"`
	Ratio  float64 `json:"ratio"`
	Status string  `json:"status"`
	Detail string  `json:"detail"`
}

func (p Progress) JSON() string {
	b, err := json.Marshal(p)
	if err != nil {
		return "null"
	}
	return string(b)
}

func defaultInstallDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "Programs", "XDL")
}

func currentExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func hasPayload() bool {
	r, err := zip.NewReader(bytes.NewReader(payloadZip), int64(len(payloadZip)))
	if err != nil {
		return false
	}
	for _, f := range r.File {
		if strings.EqualFold(path.Clean(f.Name), "xdl.exe") {
			return true
		}
	}
	return false
}

func readInstallRecord() (version, dir string, err error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return "", "", err
	}
	defer k.Close()
	version, _, _ = k.GetStringValue("DisplayVersion")
	dir, _, _ = k.GetStringValue("InstallLocation")
	if version == "" && dir == "" {
		return "", "", os.ErrNotExist
	}
	return version, dir, nil
}

func cleanInstallDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("先选择一个安装位置。")
	}
	if strings.ContainsAny(dir, "\"\r\n") {
		return "", errors.New("安装位置包含无法使用的字符。")
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return "", errors.New("安装位置需要是完整路径。")
	}
	rest := strings.TrimPrefix(dir, filepath.VolumeName(dir))
	if strings.Trim(rest, `\/`) == "" {
		return "", errors.New("不能装到磁盘根目录。选一个文件夹，例如默认的用户目录。")
	}
	windir := os.Getenv("WINDIR")
	if windir != "" && (strings.EqualFold(dir, windir) || within(windir, dir)) {
		return "", errors.New("不能装到 Windows 系统目录。")
	}
	return dir, nil
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && rel != ".."
}

func sameOrWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}

func safeRel(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", errBadPayload
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errBadPayload
	}
	return filepath.FromSlash(clean), nil
}

func install(dir string, report func(Progress)) error {
	dir, err := cleanInstallDir(dir)
	if err != nil {
		return err
	}
	if !hasPayload() {
		return errors.New("安装包里没有程序文件。请重新下载安装包。")
	}
	reader, err := zip.NewReader(bytes.NewReader(payloadZip), int64(len(payloadZip)))
	if err != nil {
		return errors.New("无法读取安装包内容。请重新下载安装包。")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("无法创建安装目录 %s：%s", dir, err.Error())
	}

	var total int64
	files := make([]struct {
		rel string
		zf  *zip.File
	}, 0, len(reader.File))
	for _, zf := range reader.File {
		if strings.HasSuffix(zf.Name, "/") {
			continue
		}
		rel, err := safeRel(zf.Name)
		if err != nil {
			return err
		}
		files = append(files, struct {
			rel string
			zf  *zip.File
		}{rel, zf})
		total += int64(zf.UncompressedSize64)
	}
	if total == 0 {
		total = 1
	}

	var written int64
	var last int64
	for _, item := range files {
		if err := extractOne(dir, item.rel, item.zf, func(n int64) {
			written += n
			if written-last >= 512*1024 || written >= total {
				last = written
				report(Progress{
					Phase:  "files",
					Ratio:  ratio(written, total),
					Status: "正在写入程序",
					Detail: formatBytes(written) + " / " + formatBytes(total),
				})
			}
		}); err != nil {
			return err
		}
	}
	report(Progress{Phase: "files", Ratio: 1, Status: "正在写入程序", Detail: formatBytes(total) + " / " + formatBytes(total)})

	report(Progress{Phase: "host", Ratio: 1, Status: "正在注册浏览器组件"})
	if err := runHelper(filepath.Join(dir, "xdl.exe"), "--install-native-host"); err != nil {
		return fmt.Errorf("程序已复制，但浏览器组件没有注册成功：%s", err.Error())
	}

	report(Progress{Phase: "shortcuts", Ratio: 1, Status: "正在创建快捷方式"})
	menu := startMenuDir()
	if err := createShortcut(filepath.Join(menu, "XDL.lnk"), filepath.Join(dir, "xdl.exe"), dir, "XDL 高速下载器"); err != nil {
		return err
	}
	if err := createShortcut(filepath.Join(menu, "卸载 XDL.lnk"), filepath.Join(dir, "uninstall.exe"), dir, "卸载 XDL"); err != nil {
		return err
	}

	report(Progress{Phase: "registry", Ratio: 1, Status: "正在写入卸载信息"})
	if err := writeInstallRecord(dir, total); err != nil {
		return err
	}
	return nil
}

func extractOne(dir, rel string, zf *zip.File, onWrite func(int64)) error {
	dest := filepath.Join(dir, rel)
	if !sameOrWithin(dir, dest) {
		return errBadPayload
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("无法创建文件夹 %s：%s", filepath.Dir(dest), err.Error())
	}
	src, err := zf.Open()
	if err != nil {
		return errors.New("无法读取安装包内容。请重新下载安装包。")
	}
	defer src.Close()

	tmp := dest + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("无法写入 %s：%s", dest, err.Error())
	}
	buf := make([]byte, 32*1024)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				os.Remove(tmp)
				return fmt.Errorf("无法写入 %s：%s", dest, werr.Error())
			}
			onWrite(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return errors.New("无法读取安装包内容。请重新下载安装包。")
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("无法写入 %s：%s", dest, err.Error())
	}
	if err := replaceFile(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func replaceFile(tmp, dest string) error {
	_ = os.Remove(dest + ".bak")
	if err := os.Rename(dest, dest+".bak"); err != nil && !os.IsNotExist(err) {
		if removeErr := os.Remove(dest); removeErr != nil {
			return errors.New("无法替换正在运行的 XDL。先退出它（含托盘图标），再安装。")
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("无法写入 %s：%s", dest, err.Error())
	}
	_ = os.Remove(dest + ".bak")
	return nil
}

func runHelper(exe string, args ...string) error {
	if _, err := os.Stat(exe); err != nil {
		return errors.New("找不到 " + filepath.Base(exe))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return errors.New("操作超时")
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

func writeInstallRecord(dir string, bytes int64) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("无法写入卸载信息：%s", err.Error())
	}
	defer k.Close()
	unins := filepath.Join(dir, "uninstall.exe")
	values := map[string]string{
		"DisplayName":     "XDL",
		"DisplayVersion":  Version,
		"Publisher":       "XDL",
		"InstallLocation": dir,
		"UninstallString": `"` + unins + `"`,
		"DisplayIcon":     filepath.Join(dir, "xdl.exe"),
	}
	for name, value := range values {
		if err := k.SetStringValue(name, value); err != nil {
			return fmt.Errorf("无法写入卸载信息：%s", err.Error())
		}
	}
	kb := uint32(bytes / 1024)
	if kb == 0 {
		kb = 1
	}
	_ = k.SetDWordValue("EstimatedSize", kb)
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	return nil
}

func uninstall(dir string, report func(Progress)) error {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || !filepath.IsAbs(dir) {
		return errors.New("找不到已安装的 XDL。")
	}
	exe := filepath.Join(dir, "xdl.exe")
	unins := filepath.Join(dir, "uninstall.exe")
	_, exeErr := os.Stat(exe)
	_, uninsErr := os.Stat(unins)
	if exeErr != nil && uninsErr != nil {
		return errors.New("这个文件夹里没有 XDL，没有删除任何文件。")
	}

	report(Progress{Phase: "host", Ratio: 0.2, Status: "正在卸下浏览器组件"})
	if exeErr == nil {
		_ = runHelper(exe, "--uninstall-native-host")
	}

	report(Progress{Phase: "files", Ratio: 0.55, Status: "正在删除程序"})
	_ = os.Remove(exe)
	_ = os.Remove(exe + ".bak")
	ext := filepath.Join(dir, "extension")
	if within(dir, ext) {
		_ = os.RemoveAll(ext)
	}

	report(Progress{Phase: "shortcuts", Ratio: 0.8, Status: "正在清理快捷方式"})
	menu := startMenuDir()
	_ = os.Remove(filepath.Join(menu, "XDL.lnk"))
	_ = os.Remove(filepath.Join(menu, "卸载 XDL.lnk"))
	_ = os.Remove(menu)
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstallKey)

	report(Progress{Phase: "registry", Ratio: 1, Status: "正在完成卸载"})
	scheduleSelfDelete(unins, dir)
	return nil
}

func scheduleSelfDelete(unins, dir string) {
	if strings.ContainsAny(unins, "\"%&\n\r") || strings.ContainsAny(dir, "\"%&\n\r") {
		return
	}
	bat := filepath.Join(os.TempDir(), "xdl-uninstall-cleanup.cmd")
	body := strings.Join([]string{
		"@echo off",
		"set /a n=0",
		":loop",
		"set /a n+=1",
		"if %n% GTR 300 goto end",
		`del /f /q "` + unins + `" >nul 2>&1`,
		`if exist "` + unins + `" (`,
		"  ping 127.0.0.1 -n 2 >nul",
		"  goto loop",
		")",
		":end",
		`rmdir "` + dir + `" >nul 2>&1`,
		`del /f /q "%~f0"`,
		"",
	}, "\r\n")
	if err := os.WriteFile(bat, []byte(body), 0o644); err != nil {
		return
	}
	cmd := exec.Command(bat)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	_ = cmd.Start()
}

func launchApp(dir string) error {
	exe := filepath.Join(dir, "xdl.exe")
	if _, err := os.Stat(exe); err != nil {
		return errors.New("找不到已安装的 XDL。")
	}
	cmd := exec.Command(exe)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法打开 XDL：%s", err.Error())
	}
	return nil
}

func startMenuDir() string {
	root := os.Getenv("APPDATA")
	if root == "" {
		root = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
	}
	return filepath.Join(root, "Microsoft", "Windows", "Start Menu", "Programs", "XDL")
}

func ratio(done, total int64) float64 {
	if total <= 0 {
		return 1
	}
	r := float64(done) / float64(total)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

func formatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB"}
	v := float64(n)
	u := -1
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	return fmt.Sprintf("%.1f %s", v, units[u])
}

func runPreviewInstall(report func(Progress)) error {
	total := int64(48 << 20)
	for n := int64(0); n <= total; n += 2 << 20 {
		report(Progress{
			Phase:  "files",
			Ratio:  ratio(n, total),
			Status: "正在写入程序",
			Detail: formatBytes(n) + " / " + formatBytes(total),
		})
		time.Sleep(50 * time.Millisecond)
	}
	report(Progress{Phase: "host", Ratio: 1, Status: "正在注册浏览器组件"})
	time.Sleep(350 * time.Millisecond)
	report(Progress{Phase: "shortcuts", Ratio: 1, Status: "正在创建快捷方式"})
	time.Sleep(280 * time.Millisecond)
	report(Progress{Phase: "registry", Ratio: 1, Status: "正在写入卸载信息"})
	time.Sleep(200 * time.Millisecond)
	return nil
}
