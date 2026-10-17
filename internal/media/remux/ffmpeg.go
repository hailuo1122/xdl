package remux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/xdl-app/xdl/internal/media/ffmpeg"
)

// RemuxTSToMP4 uses ffmpeg stream-copy when available. Returns false if ffmpeg is missing.
func RemuxTSToMP4(ctx context.Context, dest, tsPath, ffmpegPath string) (bool, error) {
	bin, err := ffmpeg.Locate(ffmpegPath)
	if err != nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	tmp := dest + ".ffmpeg.tmp"
	_ = os.Remove(tmp)
	cmd := exec.CommandContext(cctx, bin,
		"-y", "-i", tsPath,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "+faststart",
		tmp,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		return true, fmt.Errorf("ffmpeg 转封装失败：%v (%s)", err, truncate(string(out), 400))
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return true, err
	}
	return true, nil
}

// MuxAV copies video + audio elementary fragmented files into one MP4 via ffmpeg.
func MuxAV(ctx context.Context, dest, videoPath, audioPath, ffmpegPath string) (bool, error) {
	bin, err := ffmpeg.Locate(ffmpegPath)
	if err != nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	tmp := dest + ".ffmpeg.tmp"
	_ = os.Remove(tmp)
	cmd := exec.CommandContext(cctx, bin,
		"-y",
		"-i", videoPath,
		"-i", audioPath,
		"-c", "copy",
		"-map", "0:v:0",
		"-map", "1:a:0?",
		"-movflags", "+faststart",
		tmp,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		return true, fmt.Errorf("ffmpeg 音视频合并失败：%v (%s)", err, truncate(string(out), 400))
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return true, err
	}
	return true, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
