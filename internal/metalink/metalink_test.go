package metalink

import (
	"strings"
	"testing"
)

const sample4 = `<?xml version="1.0" encoding="UTF-8"?>
<metalink xmlns="urn:ietf:params:xml:ns:metalink">
  <file name="demo.bin">
    <size>12</size>
    <hash type="sha-256">e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855</hash>
    <url priority="2">https://slow.example/demo.bin</url>
    <url priority="1">https://fast.example/demo.bin</url>
    <url priority="3">ftp://files.example/demo.bin</url>
  </file>
</metalink>`

const sample3 = `<?xml version="1.0" encoding="UTF-8"?>
<metalink version="3.0" xmlns="http://www.metalinker.org/">
  <files>
    <file name="old.iso">
      <verification>
        <hash type="sha256">e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855</hash>
      </verification>
      <resources>
        <url type="https" preference="40">https://b.example/old.iso</url>
        <url type="https" preference="90">https://a.example/old.iso</url>
      </resources>
    </file>
  </files>
</metalink>`

func TestParseMetalink4Priority(t *testing.T) {
	doc, err := Parse(strings.NewReader(sample4))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Files) != 1 {
		t.Fatalf("files %d", len(doc.Files))
	}
	f := doc.Files[0]
	if f.Name != "demo.bin" || f.Size != 12 {
		t.Fatalf("file %+v", f)
	}
	if f.SHA256 == "" {
		t.Fatal("missing sha256")
	}
	if f.URLs[0] != "https://fast.example/demo.bin" {
		t.Fatalf("order %v", f.URLs)
	}
	if f.URLs[len(f.URLs)-1] != "ftp://files.example/demo.bin" {
		t.Fatalf("ftp dropped: %v", f.URLs)
	}
}

func TestParseMetalink3Preference(t *testing.T) {
	doc, err := Parse(strings.NewReader(sample3))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Files[0].URLs[0] != "https://a.example/old.iso" {
		t.Fatalf("order %v", doc.Files[0].URLs)
	}
}

func TestLooksLike(t *testing.T) {
	if !LooksLike("https://example.com/a/file.meta4?x=1") {
		t.Fatal("meta4")
	}
	if LooksLike("https://example.com/file.bin") || LooksLike("ftp://example.com/a.metalink") {
		t.Fatal("false positive")
	}
}
