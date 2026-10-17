package grab

import (
	"path"
	"regexp"
	"strings"
)

// Filter selects which candidates to keep after extraction.
type Filter struct {
	// Extensions are lower-case without dots, e.g. "zip", "mp4". Empty = any.
	Extensions []string
	// Regex is matched against the full URL. Empty = no regex filter.
	Regex string
	// MinSizeBytes is advisory (only applied when Size is known). 0 = ignore.
	MinSizeBytes int64
	// IncludePages keeps HTML page links as candidates (default false for download lists).
	IncludePages bool
}

// Match reports whether c passes the filter.
func (f Filter) Match(c Candidate) bool {
	if !f.IncludePages {
		ext := c.Ext
		if ext == "" {
			ext = PathExt(c.URL)
		}
		if ext == "" || isHTMLExt(ext) {
			// Keep media/binary-looking paths without ext only if source suggests asset.
			if c.Source == "a" && !looksLikeFileURL(c.URL) {
				return false
			}
		}
	}
	if len(f.Extensions) > 0 {
		ext := strings.ToLower(c.Ext)
		if ext == "" {
			ext = PathExt(c.URL)
		}
		ok := false
		for _, e := range f.Extensions {
			e = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e)), ".")
			if e != "" && e == ext {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.Regex != "" {
		re, err := regexp.Compile(f.Regex)
		if err != nil {
			return false
		}
		if !re.MatchString(c.URL) {
			return false
		}
	}
	return true
}

// ApplyFilter returns candidates that match f.
func ApplyFilter(cands []Candidate, f Filter) []Candidate {
	if len(cands) == 0 {
		return nil
	}
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if f.Match(c) {
			out = append(out, c)
		}
	}
	return out
}

func isHTMLExt(ext string) bool {
	switch ext {
	case "html", "htm", "php", "asp", "aspx", "jsp", "cgi":
		return true
	default:
		return false
	}
}

func looksLikeFileURL(raw string) bool {
	ext := PathExt(raw)
	if ext == "" || isHTMLExt(ext) {
		return false
	}
	base := path.Base(raw)
	return strings.Contains(base, ".")
}

// DefaultBinaryExts are common downloadable extensions for batch/grab presets.
func DefaultBinaryExts() []string {
	return []string{
		"zip", "rar", "7z", "tar", "gz", "bz2", "xz",
		"exe", "msi", "dmg", "pkg", "deb", "rpm", "apk",
		"pdf", "epub", "mobi",
		"mp4", "mkv", "webm", "avi", "mov", "m4v", "flv",
		"mp3", "flac", "wav", "aac", "ogg", "m4a",
		"jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "ico",
		"m3u8", "mpd", "ts", "m4s",
		"iso", "img", "bin",
		"doc", "docx", "xls", "xlsx", "ppt", "pptx",
		"torrent",
	}
}
