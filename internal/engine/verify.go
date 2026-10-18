package engine

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
)

// NormalizeSHA256 accepts 64 hex chars or standard base64 of 32 bytes.
func NormalizeSHA256(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "\"'")
	low := strings.ToLower(s)
	for _, prefix := range []string{"sha256:", "sha-256:", "sha-256="} {
		if strings.HasPrefix(low, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			low = strings.ToLower(s)
			break
		}
	}
	if i := strings.IndexByte(low, ' '); i > 0 && !strings.Contains(s, "=") {
		s = s[:i]
		low = low[:i]
	}
	if len(low) == 64 && isHex(low) {
		return low, true
	}
	enc := base64.StdEncoding
	if strings.Contains(s, "-") || strings.Contains(s, "_") {
		enc = base64.RawURLEncoding
		s = strings.TrimRight(s, "=")
	}
	b, err := enc.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		if b, err = base64.StdEncoding.DecodeString(strings.TrimSpace(raw)); err != nil || len(b) != sha256.Size {
			return "", false
		}
	}
	return hex.EncodeToString(b), true
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// ParseDeclaredSHA256 reads Content-Digest (RFC 9530) or Digest (RFC 3230).
func ParseDeclaredSHA256(h http.Header) string {
	if h == nil {
		return ""
	}
	for _, v := range []string{h.Get("Content-Digest"), h.Get("Digest")} {
		if sum, ok := digestValue(v); ok {
			return sum
		}
	}
	return ""
}

func digestValue(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	lower := strings.ToLower(v)
	idx := strings.Index(lower, "sha-256=")
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimSpace(v[idx+len("sha-256="):])
	if rest == "" {
		return "", false
	}
	if rest[0] == ':' {
		end := strings.IndexByte(rest[1:], ':')
		if end < 0 {
			return "", false
		}
		return NormalizeSHA256(rest[1 : 1+end])
	}
	end := strings.IndexAny(rest, ",;")
	if end >= 0 {
		rest = rest[:end]
	}
	return NormalizeSHA256(rest)
}

// NormalizeOrEmpty returns a canonical hash or empty when raw is blank or invalid.
func NormalizeOrEmpty(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	sum, ok := NormalizeSHA256(raw)
	if !ok {
		return ""
	}
	return sum
}

// HashFile returns the lowercase hex SHA-256 of path.
func HashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(LongPath(path))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			if _, err := h.Write(buf[:n]); err != nil {
				return "", err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
