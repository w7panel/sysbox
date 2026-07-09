package rootfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPreparerCreatesUpperAndWorkWithoutMeta(t *testing.T) {
	volumeRoot := t.TempDir()
	prepared, err := NewLocalPreparer().PrepareRootfsRwLayer(context.Background(), PrepareRootfsRequest{
		PVCMountPath: volumeRoot,
		Path:         "containers/app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.UpperDir != filepath.Join(volumeRoot, "containers/app/upper") {
		t.Fatalf("upper = %q", prepared.UpperDir)
	}
	if prepared.WorkDir != filepath.Join(volumeRoot, "containers/app/work") {
		t.Fatalf("work = %q", prepared.WorkDir)
	}
	if _, err := os.Stat(filepath.Join(volumeRoot, "containers/app/meta.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("meta.json err = %v, want not exist", err)
	}
}

func TestLocalPreparerRejectsUnsafePaths(t *testing.T) {
	_, err := NewLocalPreparer().PrepareRootfsRwLayer(context.Background(), PrepareRootfsRequest{
		PVCMountPath: t.TempDir(),
		Path:         "../escape",
	})
	if !errors.Is(err, ErrUnsafeLayerPath) {
		t.Fatalf("err = %v, want %v", err, ErrUnsafeLayerPath)
	}
}

func TestLocalPreparerRejectsSymlinkLayerRoot(t *testing.T) {
	volumeRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(volumeRoot, "containers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(volumeRoot, "containers/app")); err != nil {
		t.Fatal(err)
	}
	_, err := NewLocalPreparer().PrepareRootfsRwLayer(context.Background(), PrepareRootfsRequest{
		PVCMountPath: volumeRoot,
		Path:         "containers/app",
	})
	if !errors.Is(err, ErrUnsafeLayerPath) {
		t.Fatalf("err = %v, want %v", err, ErrUnsafeLayerPath)
	}
}

func TestLocalPreparerRejectsExistingNonDirectoryUpper(t *testing.T) {
	volumeRoot := t.TempDir()
	upper := filepath.Join(volumeRoot, "containers/app/upper")
	if err := os.MkdirAll(filepath.Dir(upper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upper, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewLocalPreparer().PrepareRootfsRwLayer(context.Background(), PrepareRootfsRequest{
		PVCMountPath: volumeRoot,
		Path:         "containers/app",
	})
	if !errors.Is(err, ErrUnmanagedRootfsLayer) {
		t.Fatalf("err = %v, want %v", err, ErrUnmanagedRootfsLayer)
	}
}
