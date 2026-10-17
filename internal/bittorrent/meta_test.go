package bittorrent

import "testing"

func TestMetaRoundTrip(t *testing.T) {
	in := Meta{
		InfoHash:  "abc",
		Magnet:    "magnet:?xt=urn:btih:abc",
		Name:      "demo",
		Selected:  []int{0, 2},
		SeedAfter: true,
		DataDir:   `D:\dl`,
	}
	raw, err := EncodeMeta(in)
	if err != nil {
		t.Fatal(err)
	}
	out := DecodeMeta(raw)
	if out.InfoHash != in.InfoHash || out.Magnet != in.Magnet || out.Name != in.Name {
		t.Fatalf("got %+v", out)
	}
	if len(out.Selected) != 2 || out.Selected[1] != 2 || !out.SeedAfter {
		t.Fatalf("selected/seed: %+v", out)
	}
}

func TestIsMagnetAndTorrentPath(t *testing.T) {
	if !IsMagnet("magnet:?xt=urn:btih:x") || IsMagnet("https://a") {
		t.Fatal("magnet detect")
	}
	if !IsTorrentPath(`C:\a\b.torrent`) || IsTorrentPath("file.txt") {
		t.Fatal("torrent path detect")
	}
}
