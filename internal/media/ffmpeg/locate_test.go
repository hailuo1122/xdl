package ffmpeg

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	body := []byte("xdl-ffmpeg-smoke")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	if err := VerifySHA256(path, want); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(path, "00"); err == nil {
		t.Fatal("expected mismatch")
	}
}
