package hls

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"testing"
)

func TestDecryptAES128(t *testing.T) {
	key := []byte("0123456789abcdef")
	plain := bytes.Repeat([]byte("xdl-hls-seg-"), 64) // 768 bytes
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := make([]byte, 16)
	binary.BigEndian.PutUint64(iv[8:], 3)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	enc := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(enc, padded)

	out, err := DecryptAES128(enc, key, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatalf("decrypt mismatch: got %d want %d", len(out), len(plain))
	}
}
