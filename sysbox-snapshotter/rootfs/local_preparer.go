package rootfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type LocalPreparer struct{}

func NewLocalPreparer() *LocalPreparer { return &LocalPreparer{} }

func (p *LocalPreparer) PrepareRootfsRwLayer(ctx context.Context, request PrepareRootfsRequest) (PreparedRootfs, error) {
	if err := ctx.Err(); err != nil {
		return PreparedRootfs{}, err
	}
	if request.PVCMountPath == "" {
		return PreparedRootfs{}, fmt.Errorf("pvc mount path is required")
	}
	layerRoot, err := safeJoin(request.PVCMountPath, request.Path)
	if err != nil {
		return PreparedRootfs{}, err
	}
	if err := rejectSymlink(request.PVCMountPath, layerRoot); err != nil {
		return PreparedRootfs{}, err
	}
	upper := filepath.Join(layerRoot, "upper")
	work := filepath.Join(layerRoot, "work")
	for _, path := range []string{upper, work} {
		if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return PreparedRootfs{}, ErrUnmanagedRootfsLayer
		}
	}
	if err := os.MkdirAll(upper, 0o755); err != nil {
		return PreparedRootfs{}, fmt.Errorf("create rootfs upperdir: %w", err)
	}
	if err := os.MkdirAll(work, 0o711); err != nil {
		return PreparedRootfs{}, fmt.Errorf("create rootfs workdir: %w", err)
	}
	for _, path := range []string{upper, work} {
		info, err := os.Lstat(path)
		if err != nil {
			return PreparedRootfs{}, fmt.Errorf("stat rootfs rw-layer path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return PreparedRootfs{}, ErrUnmanagedRootfsLayer
		}
	}
	return PreparedRootfs{UpperDir: upper, WorkDir: work}, nil
}

func rejectSymlink(root, path string) error {
	if err := rejectSymlinkPath(root); err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("resolve rootfs rw-layer path: %w", err)
	}
	current := root
	for _, elem := range strings.Split(rel, string(filepath.Separator)) {
		if elem == "" || elem == "." {
			continue
		}
		current = filepath.Join(current, elem)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stat rootfs rw-layer path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafeLayerPath
		}
	}
	return nil
}

func rejectSymlinkPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat rootfs rw-layer path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafeLayerPath
	}
	return nil
}
