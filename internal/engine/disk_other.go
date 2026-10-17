//go:build !windows

package engine

func diskFreeBytes(dir string) (int64, error) {
	return 0, nil
}
