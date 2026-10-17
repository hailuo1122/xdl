//go:build windows

package engine

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func diskFreeBytes(dir string) (int64, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	var free, total, totalFree uint64
	pathPtr, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return 0, err
	}
	err = windows.GetDiskFreeSpaceEx(pathPtr, &free, &total, &totalFree)
	if err != nil {
		return 0, err
	}
	return int64(free), nil
}
