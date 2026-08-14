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
	accessMountPath := localAccessPath(request.PVCMountPath)
	accessLayerRoot, err := safeJoin(accessMountPath, request.Path)
	if err != nil {
		return PreparedRootfs{}, err
	}
	if err := rejectSymlink(accessMountPath, accessLayerRoot); err != nil {
		return PreparedRootfs{}, err
	}
	upper := filepath.Join(accessLayerRoot, "upper")
	work := filepath.Join(accessLayerRoot, "work")
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
	// The mount options are consumed by runc in the L1 kubelet mount
	// namespace, so return the original kubelet-visible paths rather than the
	// /proc/1/root access paths used by a hostPID agent with a private mount
	// namespace.
	return PreparedRootfs{
		UpperDir: filepath.Join(layerRoot, "upper"),
		WorkDir:  filepath.Join(layerRoot, "work"),
	}, nil
}

// localAccessPath translates only kubelet-managed Pod volume paths through
// the L1 init root when the snapshotter shares the L1 PID namespace but not
// its mount namespace. This is the layout used by the nested-agent. Keeping
// the fallback restricted to kubelet Pod volumes prevents arbitrary OCI mount
// sources from being resolved through the L1 root.
func localAccessPath(path string) string {
	selfMountNS, selfErr := os.Stat("/proc/self/ns/mnt")
	initMountNS, initErr := os.Stat("/proc/1/ns/mnt")
	if selfErr != nil || initErr != nil || os.SameFile(selfMountNS, initMountNS) {
		return path
	}
	return kubeletPathThroughInitRoot(path, "/proc/1/root")
}

func kubeletPathThroughInitRoot(path, initRoot string) string {
	clean := filepath.Clean(path)
	kubeletPods := filepath.Clean("/var/lib/kubelet/pods")
	rel, err := filepath.Rel(kubeletPods, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Clean(initRoot) + clean
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
