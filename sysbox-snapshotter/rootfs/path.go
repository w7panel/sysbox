package rootfs

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

var (
	ErrUnsafeLayerPath         = errors.New("unsafe rootfs rw-layer path")
	ErrUnmanagedRootfsLayer    = errors.New("rootfs rw-layer directory is not managed by sysbox")
	ErrIncompatibleRootfsLayer = errors.New("rootfs rw-layer metadata is incompatible")
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
	if strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", ErrUnsafeLayerPath
	}
	return cleaned, nil
}

func safeJoin(root string, path string) (string, error) {
	cleaned, err := safeLayerPath(path)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(root, cleaned)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	joinedAbs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, joinedAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrUnsafeLayerPath
	}
	return joinedAbs, nil
}
