package main

import (
	"os"
	"path/filepath"
	"testing"
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
