package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extensionSource returns the newest built unpacked extension directory.
// The packaged internal-alpha folder sits next to xdl.exe; developer builds
// are discovered from the repository working tree.
func extensionSource() string {
	var candidates []string
	add := func(p string) {
		if p == "" {
			return
		}
		if _, err := os.Stat(filepath.Join(p, "manifest.json")); err == nil {
			candidates = append(candidates, filepath.Clean(p))
		}
	}
	if env := strings.TrimSpace(os.Getenv("XDL_EXTENSION_PATH")); env != "" {
		add(env)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		add(filepath.Join(dir, "extension"))
		add(filepath.Join(dir, "extension", ".output", "chrome-mv3"))
	}
	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, "extension", ".output", "chrome-mv3"))
		add(filepath.Join(wd, "extension"))
	}
	var best string
	var bestMod int64
	for _, c := range candidates {
		info, err := os.Stat(filepath.Join(c, "manifest.json"))
		if err != nil {
			continue
		}
		if info.ModTime().UnixNano() >= bestMod {
			best = c
			bestMod = info.ModTime().UnixNano()
		}
	}
	return best
}

// DeployExtension copies the built unpacked extension to
// <data dir>/extension so the settings/onboarding screens can point users at a
// stable location. It only replaces the target when the source is newer.
func DeployExtension() (string, error) {
	target := filepath.Join(DataDir(), "extension")
	source := extensionSource()
	if source == "" {
		if _, err := os.Stat(filepath.Join(target, "manifest.json")); err == nil {
			return target, nil
		}
		return "", fmt.Errorf("没有找到浏览器扩展构建产物，请先运行 scripts/build-alpha.ps1")
	}
	if filepath.Clean(source) == filepath.Clean(target) {
		return target, nil
	}
	srcManifest := filepath.Join(source, "manifest.json")
	dstManifest := filepath.Join(target, "manifest.json")
	srcInfo, err := os.Stat(srcManifest)
	if err != nil {
		return "", err
	}
	if dstInfo, err := os.Stat(dstManifest); err == nil && !srcInfo.ModTime().After(dstInfo.ModTime()) {
		return target, nil
	}

	staging := target + ".new"
	backup := target + ".old"
	_ = os.RemoveAll(staging)
	_ = os.RemoveAll(backup)
	if err := copyTree(source, staging); err != nil {
		_ = os.RemoveAll(staging)
		return target, fmt.Errorf("复制扩展失败：%w", err)
	}
	// Swap directories so a browser loading the old copy never sees a partial
	// file set. If the rename is blocked, keep the previous copy in place.
	if err := os.Rename(target, backup); err != nil && !os.IsNotExist(err) {
		_ = os.RemoveAll(staging)
		return target, fmt.Errorf("无法替换扩展目录（可能正被浏览器占用）：%w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		_ = os.Rename(backup, target)
		_ = os.RemoveAll(staging)
		return target, fmt.Errorf("无法启用扩展目录：%w", err)
	}
	_ = os.RemoveAll(backup)
	return target, nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, in); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
}
