package engine

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAndDigest(t *testing.T) {
	hexSum := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	got, ok := NormalizeSHA256("SHA256: " + hexSum)
	if !ok || got != hexSum {
		t.Fatalf("hex %q %v", got, ok)
	}
	h := make(http.Header)
	h.Set("Content-Digest", "sha-256=:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=:")
	if ParseDeclaredSHA256(h) != hexSum {
		t.Fatalf("content-digest %q", ParseDeclaredSHA256(h))
	}
	h = make(http.Header)
	h.Set("Digest", "SHA-256="+hexSum)
	if ParseDeclaredSHA256(h) != hexSum {
		t.Fatalf("digest hex %q", ParseDeclaredSHA256(h))
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := HashFile(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if sum != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatal(sum)
	}
}
