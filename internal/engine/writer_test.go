package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskWriterCoalescedSubmit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	w := newDiskWriter(f)
	ctx := context.Background()

	const n = 8
	payload := make([]byte, writeBufSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	for i := 0; i < n; i++ {
		bufp := getWriteBuf()
		copy(*bufp, payload)
		(*bufp)[0] = byte(i)
		if err := w.Submit(ctx, bufp, writeBufSize, int64(i*writeBufSize)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n*writeBufSize {
		t.Fatalf("len=%d", len(got))
	}
	for i := 0; i < n; i++ {
		if got[i*writeBufSize] != byte(i) {
			t.Fatalf("block %d marker", i)
		}
	}
}
