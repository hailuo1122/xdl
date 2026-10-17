package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xdl-app/xdl/internal/netx"
)

// Manifest is the HTTPS update descriptor (Plan §4.10).
type Manifest struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature,omitempty"` // hex Ed25519 over "version\\nurl\\nsha256"
	Notes     string `json:"notes,omitempty"`
	Published string `json:"published,omitempty"`
}

// CheckResult is returned to the UI.
type CheckResult struct {
	Current     string    `json:"current"`
	Latest      string    `json:"latest,omitempty"`
	UpdateAvailable bool  `json:"updateAvailable"`
	URL         string    `json:"url,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	CheckedAt   time.Time `json:"checkedAt"`
	Error       string    `json:"error,omitempty"`
}

// FetchManifest downloads and optionally verifies the update manifest.
// pubKey may be nil to skip signature verification (dev / unsigned beta).
func FetchManifest(ctx context.Context, manifestURL string, pubKey ed25519.PublicKey) (*Manifest, error) {
	manifestURL = strings.TrimSpace(manifestURL)
	if manifestURL == "" {
		return nil, fmt.Errorf("未配置更新清单 URL")
	}
	if !strings.HasPrefix(strings.ToLower(manifestURL), "https://") {
		return nil, fmt.Errorf("更新清单必须使用 HTTPS")
	}
	client := netx.NewProbeClient(20 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("清单 HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("清单 JSON 无效：%w", err)
	}
	m.Version = strings.TrimSpace(m.Version)
	m.URL = strings.TrimSpace(m.URL)
	m.SHA256 = strings.ToLower(strings.TrimSpace(m.SHA256))
	if m.Version == "" || m.URL == "" || m.SHA256 == "" {
		return nil, fmt.Errorf("清单缺少 version / url / sha256")
	}
	if !strings.HasPrefix(strings.ToLower(m.URL), "https://") {
		return nil, fmt.Errorf("更新包 URL 必须使用 HTTPS")
	}
	if len(m.SHA256) != 64 {
		return nil, fmt.Errorf("sha256 长度无效")
	}
	if pubKey != nil {
		if err := verifyManifest(m, pubKey); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

func verifyManifest(m Manifest, pub ed25519.PublicKey) error {
	sig, err := hex.DecodeString(strings.TrimSpace(m.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("更新清单签名无效")
	}
	msg := []byte(m.Version + "\n" + m.URL + "\n" + m.SHA256)
	if !ed25519.Verify(pub, msg, sig) {
		return fmt.Errorf("更新清单签名校验失败")
	}
	return nil
}

// CompareVersions returns 1 if a>b, -1 if a<b, 0 if equal (semver-ish, pre-release tags ignored for ordering of numeric parts).
func CompareVersions(a, b string) int {
	pa := parseVer(a)
	pb := parseVer(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x > y {
			return 1
		}
		if x < y {
			return -1
		}
	}
	return 0
}

func parseVer(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.ToLower(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}

// Check compares current against the remote manifest.
func Check(ctx context.Context, current, manifestURL string, pubKey ed25519.PublicKey) CheckResult {
	res := CheckResult{Current: current, CheckedAt: time.Now()}
	m, err := FetchManifest(ctx, manifestURL, pubKey)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Latest = m.Version
	res.URL = m.URL
	res.Notes = m.Notes
	res.UpdateAvailable = CompareVersions(m.Version, current) > 0
	return res
}

// HashReader returns hex SHA-256 of r.
func HashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
