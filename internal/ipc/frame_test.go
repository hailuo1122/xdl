package ipc

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","method":"app.ping","id":1}`)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %s", got)
	}
}

func TestFrameTooLarge(t *testing.T) {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], MaxMessageSize+1)
	r := bytes.NewReader(lenBuf[:])
	_, err := ReadFrame(r)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFramePartialHeader(t *testing.T) {
	r := bytes.NewReader([]byte{1, 2})
	_, err := ReadFrame(r)
	if err == nil {
		t.Fatal("expected error")
	}
}
