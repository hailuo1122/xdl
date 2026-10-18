//go:build !windows

package engine

import (
	"os"
)

// preallocatePlatform sets the logical size. On non-Windows builds we keep the
// portable Truncate path; OS-specific fallocate can be added later without
// changing the call site.
func preallocatePlatform(f *os.File, size int64) error {
	if size <= 0 {
		return nil
	}
	return f.Truncate(size)
}
