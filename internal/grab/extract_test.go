package grab

import (
	"strings"
	"testing"
)

func TestExtractHTML(t *testing.T) {
	html := `<html><body>
<a href="/dl/a.zip">Zip</a>
<img src="https://cdn.example.com/i.png">
<video src="../v.mp4"></video>
<source srcset="s.webm 1x, s2.webm 2x">
<a href="javascript:void(0)">skip</a>
</body></html>`
	cands, err := ExtractHTML(strings.NewReader(html), "https://example.com/p/index.html")
	if err != nil {
		t.Fatal(err)
	}
	urls := map[string]bool{}
	for _, c := range cands {
		urls[c.URL] = true
	}
	need := []string{
		"https://example.com/dl/a.zip",
		"https://cdn.example.com/i.png",
		"https://example.com/v.mp4",
		"https://example.com/p/s.webm",
		"https://example.com/p/s2.webm",
	}
	for _, u := range need {
		if !urls[u] {
			t.Fatalf("missing %s in %#v", u, urls)
		}
	}
}

func TestApplyFilter(t *testing.T) {
	cands := []Candidate{
		{URL: "https://a.com/a.zip", Ext: "zip", Source: "a"},
		{URL: "https://a.com/b.html", Ext: "html", Source: "a"},
		{URL: "https://a.com/c.mp4", Ext: "mp4", Source: "video"},
	}
	got := ApplyFilter(cands, Filter{Extensions: []string{"zip", "mp4"}})
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
}
