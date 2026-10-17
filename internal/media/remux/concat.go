package remux

import (
	"fmt"
	"io"
	"os"
)

// ConcatFiles writes sources into dest in order (same volume atomic rename is caller's job).
func ConcatFiles(dest string, sources []string) error {
	if len(sources) == 0 {
		return fmt.Errorf("没有可合并的分片")
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, src := range sources {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return out.Sync()
}
