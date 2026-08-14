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

func TestKubeletPathThroughInitRoot(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "pod volume",
			path: "/var/lib/kubelet/pods/pod-uid/volumes/kubernetes.io~local-volume/pvc-id",
			want: "/proc/1/root/var/lib/kubelet/pods/pod-uid/volumes/kubernetes.io~local-volume/pvc-id",
		},
		{
			name: "kubelet sibling",
			path: "/var/lib/kubelet/plugins/example",
			want: "/var/lib/kubelet/plugins/example",
		},
		{
			name: "unrelated absolute path",
			path: "/var/lib/rancher/k3s/storage/pvc-id",
			want: "/var/lib/rancher/k3s/storage/pvc-id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kubeletPathThroughInitRoot(tt.path, "/proc/1/root"); got != tt.want {
				t.Fatalf("path = %q, want %q", got, tt.want)
			}
		})
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
