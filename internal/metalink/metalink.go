// Package metalink reads Metalink 4 (RFC 5854) and Metalink 3 documents.
package metalink

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/xdl-app/xdl/internal/engine"
)

// File is one payload inside a metalink, with mirrors ordered best-first.
type File struct {
	Name   string
	Size   int64
	SHA256 string
	URLs   []string
}

// Doc is a parsed metalink.
type Doc struct {
	Files []File
}

type scoredURL struct {
	raw   string
	score int
	order int
}

// LooksLike reports whether raw is an http(s) URL for a .meta4 or .metalink file.
func LooksLike(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.HasSuffix(p, ".meta4") || strings.HasSuffix(p, ".metalink")
}

// Parse reads a Metalink 3 or 4 document. Namespace prefixes are ignored.
func Parse(r io.Reader) (*Doc, error) {
	dec := xml.NewDecoder(r)
	var doc Doc
	var cur *File
	var capture string
	var hashType string
	var urlScore int
	var urlOrder int
	var pending []scoredURL
	flushURL := func(text string) {
		if cur == nil {
			return
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		pending = append(pending, scoredURL{raw: text, score: urlScore, order: urlOrder})
		urlOrder++
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Metalink 解析失败：%w", err)
		}
		switch el := tok.(type) {
		case xml.StartElement:
			switch el.Name.Local {
			case "file":
				doc.Files = append(doc.Files, File{Name: attr(el, "name")})
				cur = &doc.Files[len(doc.Files)-1]
				pending = nil
				urlOrder = 0
			case "size":
				capture = "size"
			case "hash":
				capture = "hash"
				hashType = attr(el, "type")
			case "url":
				capture = "url"
				urlScore = urlRank(el)
			default:
				capture = ""
			}
		case xml.EndElement:
			if el.Name.Local == "file" && cur != nil {
				cur.URLs = orderURLs(pending)
				if cur.Name == "" && len(cur.URLs) > 0 {
					cur.Name = nameFromURL(cur.URLs[0])
				}
				cur = nil
				pending = nil
			}
			capture = ""
		case xml.CharData:
			text := string(el)
			if cur == nil || capture == "" {
				continue
			}
			switch capture {
			case "size":
				cur.Size = parseInt(text)
			case "hash":
				if cur.SHA256 == "" {
					if sum, ok := hashSHA256(hashType, text); ok {
						cur.SHA256 = sum
					}
				}
			case "url":
				flushURL(text)
				capture = ""
			}
		}
	}
	out := doc.Files[:0]
	for _, f := range doc.Files {
		if len(f.URLs) == 0 {
			continue
		}
		out = append(out, f)
	}
	doc.Files = out
	if len(doc.Files) == 0 {
		return nil, fmt.Errorf("Metalink 里没有可用的文件地址")
	}
	return &doc, nil
}

func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

func urlRank(el xml.StartElement) int {
	if p := attr(el, "priority"); p != "" {
		n := parseInt(p)
		if n < 0 {
			n = 0
		}
		return 1_000_000 - int(n)
	}
	if p := attr(el, "preference"); p != "" {
		return int(parseInt(p))
	}
	return 0
}

func orderURLs(in []scoredURL) []string {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].score == in[j].score {
			return in[i].order < in[j].order
		}
		return in[i].score > in[j].score
	})
	seen := map[string]struct{}{}
	var out []string
	for _, u := range in {
		key := strings.ToLower(strings.TrimSpace(u.raw))
		if _, ok := seen[key]; ok {
			continue
		}
		if !usableURL(u.raw) {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(u.raw))
	}
	return out
}

func usableURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp":
		return true
	default:
		return false
	}
}

func hashSHA256(kind, text string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(kind))
	k = strings.ReplaceAll(k, "-", "")
	if k != "sha256" {
		return "", false
	}
	return engine.NormalizeSHA256(text)
}

func parseInt(s string) int64 {
	s = strings.TrimSpace(s)
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func nameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.Path
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	p, _ = url.PathUnescape(p)
	return p
}
