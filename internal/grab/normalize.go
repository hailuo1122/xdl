package grab

import (
	"net/url"
	"sort"
	"strings"
)

// trackingParams are stripped so the same resource behind different campaign
// links collapses to one candidate.
var trackingParams = map[string]struct{}{
	"utm_source": {}, "utm_medium": {}, "utm_campaign": {}, "utm_term": {},
	"utm_content": {}, "utm_id": {}, "utm_reader": {}, "utm_name": {},
	"fbclid": {}, "gclid": {}, "gclsrc": {}, "dclid": {}, "msclkid": {},
	"mc_cid": {}, "mc_eid": {}, "yclid": {}, "_ga": {}, "ref": {}, "ref_": {},
}

// NormalizeURL parses, strips fragment/tracking params, and returns a stable
// string suitable for dedup. Non-http(s) URLs return an empty string.
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	u.Fragment = ""
	u.RawFragment = ""
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	if u.Port() != "" {
		host = host + ":" + u.Port()
	}
	u.Host = host
	u.Scheme = scheme

	q := u.Query()
	changed := false
	for k := range q {
		lk := strings.ToLower(k)
		if _, drop := trackingParams[lk]; drop {
			q.Del(k)
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	// Drop default ports.
	if (scheme == "http" && u.Port() == "80") || (scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
	}
	out := u.String()
	// Prefer no trailing slash on bare origins for stable keys.
	if u.Path == "/" && u.RawQuery == "" {
		out = strings.TrimRight(out, "/")
	}
	return out
}

// Dedup preserves first-seen order after NormalizeURL.
func Dedup(urls []string) []string {
	seen := make(map[string]struct{}, len(urls))
	out := make([]string, 0, len(urls))
	for _, raw := range urls {
		n := NormalizeURL(raw)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// SameOrigin reports whether a and b share scheme+host(+port).
func SameOrigin(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Host, ub.Host)
}

// PathExt returns the lower-cased file extension of the URL path (no dot), or "".
func PathExt(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := u.Path
	i := strings.LastIndex(p, ".")
	if i < 0 || i == len(p)-1 {
		return ""
	}
	ext := strings.ToLower(p[i+1:])
	if strings.ContainsAny(ext, "/?#") {
		return ""
	}
	return ext
}

// SortURLs sorts for stable UI display.
func SortURLs(urls []string) {
	sort.Strings(urls)
}
