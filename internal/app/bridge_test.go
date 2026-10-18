package app

import (
	"strings"
	"testing"
)

func TestAddRequestHeadersAllowlist(t *testing.T) {
	req := AddRequest{
		Referer:       "https://example.com/page",
		UserAgent:     "TestUA/1.0",
		Cookie:        "a=1; b=2",
		Authorization: "Bearer token",
		Headers: map[string]string{
			"X-Evil":   "drop me",
			"Range":    "bytes=0-1",
			"Accept":   "application/octet-stream",
			"Host":     "evil.example",
			"Cookie":   "c=3",
			"Origin":   "https://example.com",
			"Referer ": "",
		},
	}
	h, err := req.headers()
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Get("User-Agent"); got != "TestUA/1.0" {
		t.Fatalf("UA = %q", got)
	}
	if got := h.Get("Cookie"); !strings.Contains(got, "a=1") || !strings.Contains(got, "c=3") {
		t.Fatalf("Cookie = %q", got)
	}
	for _, forbidden := range []string{"X-Evil", "Range", "Host"} {
		if got := h.Get(forbidden); got != "" {
			t.Fatalf("%s should be dropped, got %q", forbidden, got)
		}
	}
	if got := h.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestAddRequestHeadersRejectInjection(t *testing.T) {
	req := AddRequest{
		Headers: map[string]string{"User-Agent": "good\r\nX-Injected: 1"},
	}
	if _, err := req.headers(); err == nil {
		t.Fatal("newline header injection must fail")
	}
}

func TestAddRequestHeadersBudget(t *testing.T) {
	req := AddRequest{Cookie: strings.Repeat("k=v; ", 7000)}
	if _, err := req.headers(); err == nil {
		t.Fatal("oversized headers must fail")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"../../evil.bin":     "evil.bin",
		`..\..\evil.bin`:     "evil.bin",
		`C:\temp\report.zip`: "report.zip",
		"CON":                "_CON",
		"a:b?.txt":           "a_b_.txt",
		"  spaced  .txt  ":   "spaced  .txt",
		"":                   "",
	}
	for in, want := range cases {
		got := sanitizeName(in)
		if got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeURL(t *testing.T) {
	for _, u := range []string{"http://a/b", "HTTPS://a/b", "https://a/b?x=1", "magnet:?xt=urn:btih:x", "ftp://a/b"} {
		if !safeURL(u) {
			t.Errorf("%q should be accepted", u)
		}
	}
	for _, u := range []string{"", "file:///etc/passwd", "javascript:alert(1)", "data:text/plain,x", "sftp://a/b", "http:/short"} {
		if safeURL(u) {
			t.Errorf("%q should be rejected", u)
		}
	}
}

func TestNormalizeURLForCompare(t *testing.T) {
	cases := map[string]string{
		"HTTPS://Example.COM/a/file.zip?utm_source=x&b=2&a=1#frag": "https://example.com/a/file.zip?a=1&b=2",
		"https://example.com/a?fbclid=1&gclid=2":                   "https://example.com/a",
		"https://example.com/a?x=1":                                "https://example.com/a?x=1",
		"  https://example.com/a  ":                                "https://example.com/a",
	}
	for in, want := range cases {
		if got := normalizeURLForCompare(in); got != want {
			t.Errorf("normalizeURLForCompare(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasPrefixFold(t *testing.T) {
	if !hasPrefixFold("HTTP://Example", "http://") {
		t.Fatal("case-insensitive prefix failed")
	}
	if hasPrefixFold("httpx", "http://") {
		t.Fatal("unexpected match")
	}
}
