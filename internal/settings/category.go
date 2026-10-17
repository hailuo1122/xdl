package settings

import (
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// CategoryRule maps extension/domain patterns to a named subfolder under DownloadDir.
type CategoryRule struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions,omitempty"` // without dots
	Domains    []string `json:"domains,omitempty"`    // host suffix match
	Subdir     string   `json:"subdir,omitempty"`     // relative to downloadDir
}

// DefaultCategories is the built-in auto-sort preset.
func DefaultCategories() []CategoryRule {
	return []CategoryRule{
		{Name: "视频", Extensions: []string{"mp4", "mkv", "webm", "avi", "mov", "m4v", "flv", "ts"}, Subdir: "Videos"},
		{Name: "音频", Extensions: []string{"mp3", "flac", "wav", "aac", "ogg", "m4a"}, Subdir: "Music"},
		{Name: "图片", Extensions: []string{"jpg", "jpeg", "png", "gif", "webp", "bmp", "svg"}, Subdir: "Images"},
		{Name: "文档", Extensions: []string{"pdf", "epub", "mobi", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt"}, Subdir: "Documents"},
		{Name: "压缩包", Extensions: []string{"zip", "rar", "7z", "tar", "gz", "bz2", "xz"}, Subdir: "Archives"},
		{Name: "程序", Extensions: []string{"exe", "msi", "dmg", "pkg", "deb", "rpm", "apk"}, Subdir: "Programs"},
	}
}

// MatchCategory picks the first matching rule for url/name.
func MatchCategory(rules []CategoryRule, rawURL, name string) (rule CategoryRule, ok bool) {
	ext := extFromName(name)
	if ext == "" {
		ext = extFromURL(rawURL)
	}
	host := hostOf(rawURL)
	for _, r := range rules {
		if matchExt(r.Extensions, ext) || matchDomain(r.Domains, host) {
			return r, true
		}
	}
	return CategoryRule{}, false
}

// ResolveDir returns downloadDir or downloadDir/subdir for the matched category.
func ResolveDir(downloadDir string, rules []CategoryRule, auto bool, rawURL, name string) (dir, category string) {
	dir = downloadDir
	if !auto || len(rules) == 0 {
		return dir, ""
	}
	r, ok := MatchCategory(rules, rawURL, name)
	if !ok {
		return dir, ""
	}
	sub := strings.TrimSpace(r.Subdir)
	if sub == "" {
		sub = r.Name
	}
	sub = filepath.Clean(sub)
	if sub == "." || strings.HasPrefix(sub, "..") || filepath.IsAbs(sub) {
		return dir, r.Name
	}
	return filepath.Join(downloadDir, sub), r.Name
}

func extFromName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	i := strings.LastIndex(name, ".")
	if i < 0 || i == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[i+1:])
}

func extFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return extFromName(u.Path)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func matchExt(list []string, ext string) bool {
	if ext == "" {
		return false
	}
	for _, e := range list {
		e = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e)), ".")
		if e == ext {
			return true
		}
	}
	return false
}

func matchDomain(list []string, host string) bool {
	if host == "" {
		return false
	}
	for _, d := range list {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "*.")
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
