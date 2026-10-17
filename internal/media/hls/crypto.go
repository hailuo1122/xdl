package hls

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// DecryptAES128 decrypts an HLS AES-128 CBC segment. IV is either the playlist
// hex value (0x…) or derived from the media sequence number when empty.
func DecryptAES128(cipherText, key []byte, ivHex string, mediaSeq uint64) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("AES-128 密钥长度必须为 16 字节，实际 %d", len(key))
	}
	if len(cipherText) == 0 {
		return cipherText, nil
	}
	if len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度不是 AES 块大小的整数倍")
	}
	iv, err := parseIV(ivHex, mediaSeq)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, cipherText)
	return pkcs7Unpad(out)
}

func parseIV(ivHex string, mediaSeq uint64) ([]byte, error) {
	ivHex = strings.TrimSpace(ivHex)
	if ivHex == "" {
		iv := make([]byte, 16)
		binary.BigEndian.PutUint64(iv[8:], mediaSeq)
		return iv, nil
	}
	ivHex = strings.TrimPrefix(strings.ToLower(ivHex), "0x")
	raw, err := hex.DecodeString(ivHex)
	if err != nil {
		return nil, fmt.Errorf("无效的 IV：%w", err)
	}
	if len(raw) != 16 {
		return nil, fmt.Errorf("IV 必须为 16 字节")
	}
	return raw, nil
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("空明文")
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(b) {
		return nil, fmt.Errorf("PKCS7 填充无效")
	}
	for i := 0; i < pad; i++ {
		if b[len(b)-1-i] != byte(pad) {
			return nil, fmt.Errorf("PKCS7 填充无效")
		}
	}
	return b[:len(b)-pad], nil
}
