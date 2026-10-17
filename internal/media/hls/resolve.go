package hls

import (
	"net/url"
	"path"
	"strings"
)

// ResolveURL joins a possibly-relative reference against a base playlist URL.
func ResolveURL(base, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(u).String(), nil
}

// SuggestName derives a download leaf name from a playlist URL and quality label.
func SuggestName(playlistURL, label, ext string) string {
	u, err := url.Parse(playlistURL)
	stem := "video"
	if err == nil {
		base := path.Base(u.Path)
		base = strings.TrimSuffix(base, path.Ext(base))
		if base != "" && base != "." && base != "/" {
			stem = base
		}
	}
	if label != "" {
		stem = stem + " [" + sanitizeLabel(label) + "]"
	}
	if ext == "" {
		ext = ".ts"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return stem + ext
}

func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	repl := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return repl.Replace(s)
}
