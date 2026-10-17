package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	illegalChars  = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	reservedNames = map[string]struct{}{
		"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
		"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
		"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
		"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
		"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
	}
)

const partSuffix = ".xdlpart"

// SanitizeFilename makes a Windows-safe file name.
func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "download"
	}
	name = illegalChars.ReplaceAllString(name, "_")
	name = strings.TrimRight(name, " .")
	if name == "" {
		return "download"
	}
	base := name
	ext := ""
	if i := strings.LastIndex(name, "."); i > 0 {
		base = name[:i]
		ext = name[i:]
	}
	upper := strings.ToUpper(base)
	if _, ok := reservedNames[upper]; ok {
		base = "_" + base
	}
	out := base + ext
	// Keep under MAX_PATH-ish leaf; long path prefix handled separately.
	if utf8.RuneCountInString(out) > 200 {
		runes := []rune(out)
		out = string(runes[:200])
	}
	return out
}

// UniquePath returns path, or path with (n) before extension if taken.
func UniquePath(dir, name string) (string, error) {
	name = SanitizeFilename(name)
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate, nil
	} else if err != nil && !os.IsExist(err) {
		// Stat failed for other reason; still try candidate.
	} else if err == nil {
		// exists
	}

	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		alt := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		p := filepath.Join(dir, alt)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p, nil
		}
	}
	return "", fmt.Errorf("could not find unique name for %s", name)
}

func PartPath(finalPath string) string {
	return finalPath + partSuffix
}

// LongPath prefixes \\?\ on Windows when needed.
func LongPath(p string) string {
	if len(p) < 4 {
		return p
	}
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)
	if len(abs) >= 260 {
		if strings.HasPrefix(abs, `\\`) {
			return `\\?\UNC\` + strings.TrimPrefix(abs, `\\`)
		}
		return `\\?\` + abs
	}
	return abs
}

// Preallocate truncates the file to size (sparse-friendly, no privilege needed).
func Preallocate(path string, size int64) error {
	f, err := os.OpenFile(LongPath(path), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(size)
}

// EnsureDiskSpace checks free space with 5% margin. Returns nil if unknown.
func EnsureDiskSpace(dir string, need int64) error {
	free, err := diskFreeBytes(dir)
	if err != nil || free <= 0 {
		return nil // best-effort on unsupported platforms
	}
	margin := free / 20 // 5%
	if free-margin < need {
		return NewTaskError(ErrCodeDiskFull,
			fmt.Sprintf("需要约 %d 字节，可用 %d", need, free),
			"清理磁盘空间或更换下载目录")
	}
	return nil
}
