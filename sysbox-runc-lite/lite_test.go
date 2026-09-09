package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestDirEmptyIgnoresLostFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "lost+found"), 0o700); err != nil {
		t.Fatal(err)
	}
	empty, err := dirEmpty(dir)
	if err != nil || !empty {
		t.Fatalf("dirEmpty() = %v, %v; want true, nil", empty, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	empty, err = dirEmpty(dir)
	if err != nil || empty {
		t.Fatalf("dirEmpty() = %v, %v; want false, nil", empty, err)
	}
}

func TestInitLiteVolumesWithoutAnnotation(t *testing.T) {
	podsDir, rootfs := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootfs, "data", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "data", "nested", "image.txt"), []byte("image"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(podsDir, "pod-uid", "volumes", "kubernetes.io~csi", "data", "mount")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := &specs.Spec{
		Root: &specs.Root{Path: rootfs},
		Annotations: map[string]string{
			liteContainerName: "app",
			liteSandboxUID:    "pod-uid",
		},
		Mounts: []specs.Mount{{Source: source, Destination: "/data", Type: "bind", Options: []string{"rw"}}},
	}
	if err := initLiteVolumesAt(spec, podsDir); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, filepath.Join(source, "nested", "image.txt"))); got != "image" {
		t.Fatalf("initialized content = %q", got)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "image.txt"), []byte("persistent"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := initLiteVolumesAt(spec, podsDir); err != nil {
		t.Fatal(err)
	}
	if got := string(mustRead(t, filepath.Join(source, "nested", "image.txt"))); got != "persistent" {
		t.Fatalf("existing content overwritten: %q", got)
	}
}

func TestDetectLitePVCSourceSkipsNonCSIAndValidatesSubPath(t *testing.T) {
	podsDir := t.TempDir()
	emptyDir := filepath.Join(podsDir, "pod-uid", "volumes", "kubernetes.io~empty-dir", "cache")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := detectLitePVCSourceAt(emptyDir, "pod-uid", "app", podsDir); ok {
		t.Fatal("emptyDir must not be treated as PVC")
	}
	if err := os.MkdirAll(filepath.Join(podsDir, "pod-uid", "volumes", "kubernetes.io~csi", "data", "mount"), 0o755); err != nil {
		t.Fatal(err)
	}
	subpath := filepath.Join(podsDir, "pod-uid", "volume-subpaths", "data", "app", "0")
	if err := os.MkdirAll(subpath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := detectLitePVCSourceAt(subpath, "pod-uid", "app-123", podsDir); !ok {
		t.Fatal("CSI subPath must be detected")
	}
}

func TestCopyDirPreservesSymlinkAndMode(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "dst")
	if err := os.Mkdir(filepath.Join(src, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, "nested", "file")
	if err := os.WriteFile(file, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nested/file", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.Stat(filepath.Join(dst, "nested", "file"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(dst, "nested", "file"))) != "payload" || copied.Mode().Perm() != info.Mode().Perm() {
		t.Fatalf("copied file content or mode mismatch: %q %o", mustRead(t, filepath.Join(dst, "nested", "file")), copied.Mode().Perm())
	}
	link, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil || link != "nested/file" {
		t.Fatalf("copied link = %q, %v", link, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
