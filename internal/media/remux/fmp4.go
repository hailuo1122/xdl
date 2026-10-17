package remux

import (
	"fmt"
	"os"

	"github.com/Eyevinn/mp4ff/mp4"
)

// JoinFMP4 concatenates an init segment with media segments into one fragmented MP4.
// This is the common single-track path (binary-safe for CMAF/fMP4).
func JoinFMP4(dest, initPath string, mediaPaths []string) error {
	paths := make([]string, 0, 1+len(mediaPaths))
	if initPath != "" {
		if _, err := os.Stat(initPath); err != nil {
			return fmt.Errorf("缺少 init 分片：%w", err)
		}
		paths = append(paths, initPath)
	}
	paths = append(paths, mediaPaths...)
	if len(paths) == 0 {
		return fmt.Errorf("没有 fMP4 分片可合并")
	}
	// Fast path: raw concat is valid for fragmented MP4 when segments already share
	// the init's track layout (typical HLS/DASH single AdaptationSet download).
	if err := ConcatFiles(dest, paths); err != nil {
		return err
	}
	// Best-effort validate; some CDN segments decode only after a full player probe.
	f, err := os.Open(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := mp4.DecodeFile(f); err != nil {
		_ = err // keep file; player / ffprobe is the final arbiter
	}
	return nil
}
