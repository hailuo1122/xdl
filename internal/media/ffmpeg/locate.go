package ffmpeg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Locate finds ffmpeg on PATH, then an explicit path, then %LOCALAPPDATA%\XDL\bin.
// The binary is optional; callers must handle a missing result.
func Locate(explicit string) (string, error) {
	if explicit != "" {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			return explicit, nil
		}
		return "", fmt.Errorf("指定的 ffmpeg 不存在: %s", explicit)
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("未找到 ffmpeg")
	}
	// Plan stores the optional component under LocalAppData\XDL\bin.
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local = base
	}
	candidate := filepath.Join(local, "XDL", "bin", "ffmpeg.exe")
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate, nil
	}
	return "", fmt.Errorf("未找到 ffmpeg，可在设置中指定路径或稍后按需下载")
}

// VerifySHA256 compares a file to an expected lowercase hex digest.
func VerifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("SHA-256 不匹配")
	}
	return nil
}
