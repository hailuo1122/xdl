package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xdl-app/xdl/internal/nativehost"
)

// RepairNativeHost installs the short-lived native messaging host copy, writes
// its manifest with the fixed extension origin, and registers it for installed
// Chromium browsers. Safe to call repeatedly (installer / repair button).
func RepairNativeHost() error {
	dir := NativeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位程序失败：%w", err)
	}
	hostExe := filepath.Join(dir, "xdl-native-host.exe")
	if !strings.EqualFold(filepath.Clean(exe), filepath.Clean(hostExe)) {
		if err := copyExecutable(exe, hostExe); err != nil {
			return fmt.Errorf("安装浏览器组件失败：%w", err)
		}
		if err := nativehost.WriteAppPath(dir, exe); err != nil {
			return err
		}
	}
	manifest, err := nativehost.WriteManifest(dir, hostExe, nativehost.AllowedOrigins())
	if err != nil {
		return err
	}
	if err := nativehost.InstallRegistry(manifest); err != nil {
		return fmt.Errorf("写入浏览器注册表失败：%w", err)
	}
	return nil
}

// UninstallNativeHost removes registry entries and the manifest. The host copy
// is left in place (it may be the running executable) and is harmless.
func UninstallNativeHost() error {
	if err := nativehost.UninstallRegistry(); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(NativeDir(), nativehost.HostName+".json"))
	_ = os.Remove(filepath.Join(NativeDir(), nativehost.AppPathSidecar))
	return nil
}

// EnsureNativeHostCurrent repairs the common "app updated, host copy is old"
// case without installing the integration for users who never enabled it.
// Manifest and registry are rewritten on every start; the host copy itself is
// refreshed only when the GUI executable changed.
func EnsureNativeHostCurrent() error {
	dir := NativeDir()
	st := nativehost.QueryStatus(dir)
	// Only heal a registration that belongs to this data directory. With an
	// isolated XDL_DATA_DIR the registry may still point at another install;
	// silently rewriting it there would be surprising.
	if !st.Installed || !st.ManifestExists {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位程序失败：%w", err)
	}
	hostExe := filepath.Join(dir, "xdl-native-host.exe")
	refresh := true
	if src, err := os.Stat(exe); err == nil {
		if dst, err := os.Stat(hostExe); err == nil && src.Size() == dst.Size() && !src.ModTime().After(dst.ModTime()) {
			refresh = false
		}
	}
	if refresh {
		if err := copyExecutable(exe, hostExe); err != nil {
			return err
		}
		if err := nativehost.WriteAppPath(dir, exe); err != nil {
			return err
		}
	}
	manifest, err := nativehost.WriteManifest(dir, hostExe, nativehost.AllowedOrigins())
	if err != nil {
		return err
	}
	return nativehost.InstallRegistry(manifest)
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		// A previous host process may still be running; retry after removing
		// the old copy. If the file is locked the caller gets a clear error.
		if rmErr := os.Remove(dst); rmErr == nil {
			if err := os.Rename(tmp, dst); err == nil {
				return nil
			}
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("无法更新宿主动程序（请关闭浏览器后重试）：%w", err)
	}
	return nil
}
