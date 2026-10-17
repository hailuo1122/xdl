package grab

import (
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Candidate is one downloadable resource discovered on a page.
type Candidate struct {
	URL    string `json:"url"`
	Text   string `json:"text,omitempty"`
	Source string `json:"source"` // a | img | video | source | link
	Ext    string `json:"ext,omitempty"`
	Page   string `json:"page,omitempty"`
}

// ExtractHTML walks the document and collects <a>/<img>/<video>/<source>/<link>
// absolute http(s) URLs. base is the page URL used to resolve relatives.
func ExtractHTML(r io.Reader, base string) ([]Candidate, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	seen := map[string]struct{}{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "a":
				if href := attr(n, "href"); href != "" {
					addCandidate(&out, seen, baseURL, href, textContent(n), "a", base)
				}
			case "img":
				if src := attr(n, "src"); src != "" {
					addCandidate(&out, seen, baseURL, src, attr(n, "alt"), "img", base)
				}
				if srcset := attr(n, "srcset"); srcset != "" {
					for _, u := range parseSrcset(srcset) {
						addCandidate(&out, seen, baseURL, u, "", "img", base)
					}
				}
			case "video", "audio":
				if src := attr(n, "src"); src != "" {
					addCandidate(&out, seen, baseURL, src, "", n.Data, base)
				}
				if poster := attr(n, "poster"); poster != "" {
					addCandidate(&out, seen, baseURL, poster, "", "img", base)
				}
			case "source":
				if src := attr(n, "src"); src != "" {
					addCandidate(&out, seen, baseURL, src, "", "source", base)
				}
				if srcset := attr(n, "srcset"); srcset != "" {
					for _, u := range parseSrcset(srcset) {
						addCandidate(&out, seen, baseURL, u, "", "source", base)
					}
				}
			case "link":
				rel := strings.ToLower(attr(n, "rel"))
				if strings.Contains(rel, "stylesheet") || strings.Contains(rel, "icon") ||
					strings.Contains(rel, "preload") || strings.Contains(rel, "alternate") {
					if href := attr(n, "href"); href != "" {
						addCandidate(&out, seen, baseURL, href, "", "link", base)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

// ExtractPageLinks returns only <a href> URLs (for crawl frontier expansion).
func ExtractPageLinks(r io.Reader, base string) ([]string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]struct{}{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			if href := attr(n, "href"); href != "" {
				abs := resolve(baseURL, href)
				nurl := NormalizeURL(abs)
				if nurl == "" {
					return
				}
				if _, ok := seen[nurl]; ok {
					return
				}
				seen[nurl] = struct{}{}
				out = append(out, nurl)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

func addCandidate(out *[]Candidate, seen map[string]struct{}, base *url.URL, raw, text, source, page string) {
	abs := resolve(base, raw)
	nurl := NormalizeURL(abs)
	if nurl == "" {
		return
	}
	if _, ok := seen[nurl]; ok {
		return
	}
	seen[nurl] = struct{}{}
	*out = append(*out, Candidate{
		URL:    nurl,
		Text:   strings.TrimSpace(text),
		Source: source,
		Ext:    PathExt(nurl),
		Page:   page,
	})
}

func resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "javascript:") || strings.HasPrefix(ref, "data:") ||
		strings.HasPrefix(ref, "mailto:") || strings.HasPrefix(ref, "tel:") || strings.HasPrefix(ref, "#") {
		return ""
	}
	u, err := base.Parse(ref)
	if err != nil {
		return ""
	}
	return u.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
		for x := c.FirstChild; x != nil; x = x.NextSibling {
			walk(x)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func parseSrcset(srcset string) []string {
	parts := strings.Split(srcset, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		fields := strings.Fields(p)
		if len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}
