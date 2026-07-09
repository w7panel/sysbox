package rootfs

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

var (
	ErrUnsafeLayerPath      = errors.New("unsafe rootfs rw-layer path")
	ErrUnmanagedRootfsLayer = errors.New("rootfs rw-layer directory is not managed by sysbox")
)

func safeLayerPath(path string) (string, error) {
	if path == "" || path == "." {
		return "", nil
	}
	if filepath.IsAbs(path) {
		return "", ErrUnsafeLayerPath
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return "", nil
	}
	if slices.Contains(strings.Split(cleaned, string(filepath.Separator)), "..") {
		return "", ErrUnsafeLayerPath
	}
	return cleaned, nil
}

func safeJoin(root, path string) (string, error) {
	cleaned, err := safeLayerPath(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(root, cleaned))
}
