package grab

import "testing"

func TestNormalizeURL_stripsTrackers(t *testing.T) {
	in := "https://Example.COM/path/file.zip?utm_source=x&id=1#frag"
	got := NormalizeURL(in)
	want := "https://example.com/path/file.zip?id=1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDedup(t *testing.T) {
	in := []string{
		"https://a.com/f.zip",
		"https://a.com/f.zip?utm_campaign=1",
		"ftp://nope",
		"https://a.com/g.zip",
	}
	got := Dedup(in)
	if len(got) != 2 {
		t.Fatalf("len=%d got=%v", len(got), got)
	}
}

func TestSameOrigin(t *testing.T) {
	if !SameOrigin("https://a.com/x", "https://a.com/y") {
		t.Fatal("expected same")
	}
	if SameOrigin("https://a.com/x", "http://a.com/x") {
		t.Fatal("scheme differs")
	}
}
