//go:build linux

package sysboxsnapshotter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"golang.org/x/sys/unix"
)

func TestExtractionMountsSkipRootfsHook(t *testing.T) {
	o := &snapshotter{root: t.TempDir(), rootfsHooks: RootfsHooks{
		IdentityResolver: fakeIdentityResolver{err: errors.New("must not resolve extraction identity")},
		MetadataResolver: fakeMetadataResolver{}, PVCResolver: fakePVCResolver{}, Preparer: fakePreparer{},
	}}
	mounts, err := o.mounts(context.Background(), "extract-layer", storage.Snapshot{
		Kind: snapshots.KindActive, ID: "2", ParentIDs: []string{"1"},
	}, snapshots.Info{Labels: map[string]string{"containerd.io/snapshot.ref": "sha256:layer"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 || mounts[0].Type != "fuse3.fuse-overlayfs" {
		t.Fatalf("extraction must keep FUSE layers: %#v", mounts)
	}
}

func TestCopyRootfsPreservesImageMetadata(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	file := filepath.Join(src, "file")
	if err := os.WriteFile(file, []byte("image data"), 0751); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(src, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(src, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(file, "user.sysbox-test", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	if err := copyRootfs(context.Background(), dst, src); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "file"))
	if err != nil || string(got) != "image data" {
		t.Fatalf("copied data %q: %v", got, err)
	}
	first, _ := os.Stat(filepath.Join(dst, "file"))
	second, _ := os.Stat(filepath.Join(dst, "hardlink"))
	if !os.SameFile(first, second) || first.Mode().Perm() != 0751 {
		t.Fatal("hardlinks or mode lost")
	}
	source, _ := os.Stat(file)
	a, b := source.Sys().(*syscall.Stat_t), first.Sys().(*syscall.Stat_t)
	if a.Uid != b.Uid || a.Gid != b.Gid {
		t.Fatal("ownership lost")
	}
	if link, err := os.Readlink(filepath.Join(dst, "symlink")); err != nil || link != "file" {
		t.Fatalf("symlink %q: %v", link, err)
	}
	value := make([]byte, 32)
	n, err := unix.Getxattr(filepath.Join(dst, "file"), "user.sysbox-test", value)
	if err != nil || string(value[:n]) != "value" {
		t.Fatalf("xattr lost: %v", err)
	}
}

func TestCopyRootfsCancellationWaitsForProcess(t *testing.T) {
	bin := t.TempDir()
	pidfile := filepath.Join(bin, "pid")
	// exec keeps a single PID, just like cp, so cancellation must reap it.
	if err := os.WriteFile(filepath.Join(bin, "cp"), []byte("#!/bin/sh\necho $$ > \"$COPY_PIDFILE\"\nexec /bin/sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("COPY_PIDFILE", pidfile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- copyRootfs(ctx, "unused", "unused") }()
	deadline := time.After(5 * time.Second)
	var pid int
	for pid == 0 {
		if data, err := os.ReadFile(pidfile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		select {
		case <-deadline:
			t.Fatal("copy process did not start")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("copy error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copy process survived cancellation")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("copy process was not reaped: %v", err)
	}
}

func TestSnapshotCleanupRequiresSuccessfulUnmount(t *testing.T) {
	for _, fail := range []bool{true, false} {
		dir := t.TempDir()
		file := filepath.Join(dir, "image-file")
		if err := os.WriteFile(file, []byte("do not delete while mounted"), 0600); err != nil {
			t.Fatal(err)
		}
		err := removeSnapshotDirectory(dir, func(path string, flags int) error {
			if path != filepath.Join(dir, "merged") || flags != 0 {
				t.Fatalf("unexpected unmount %s %d", path, flags)
			}
			if fail {
				return syscall.EBUSY
			}
			return nil
		})
		_, statErr := os.Stat(file)
		if fail && (!errors.Is(err, syscall.EBUSY) || statErr != nil) {
			t.Fatalf("failed unmount must retain files: %v, %v", err, statErr)
		}
		if !fail && (err != nil || !os.IsNotExist(statErr)) {
			t.Fatalf("successful unmount must allow cleanup: %v, %v", err, statErr)
		}
	}
}

func TestMaterializationMarkerSurvivesInterruptedPublish(t *testing.T) {
	root := t.TempDir()
	upper, staged := filepath.Join(root, "upper"), filepath.Join(root, "staged")
	marker := filepath.Join(root, "marker")
	for _, dir := range []string{upper, staged} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(upper, "original"), []byte("PVC data"), 0600); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		got, err := materializedRootfsComplete(marker, upper)
		if err != nil || got != want {
			t.Fatalf("complete = %v, want %v: %v", got, want, err)
		}
	}
	check(false)
	data, err := rootfsInodeMarker(staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	// Crash after recording the future inode, before exchange: retry the copy.
	check(false)
	if _, err := os.Stat(filepath.Join(upper, "original")); err != nil {
		t.Fatal("original PVC data lost before publish", err)
	}
	if err := publishMaterializedRootfs(staged, upper, marker); err != nil {
		t.Fatal(err)
	}
	// Crash just after exchange: no extra write is needed to recognize success.
	check(true)
	if _, err := os.Stat(filepath.Join(staged, "original")); err != nil {
		t.Fatal("original PVC data not retained until publication", err)
	}
	if _, err := os.Stat(filepath.Join(upper, "original")); !os.IsNotExist(err) {
		t.Fatal("published tree unexpectedly retains removed file", err)
	}
	if err := os.WriteFile(marker, []byte("materialized\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check(true) // v33 marker compatibility.
}

func TestFailedMaterializationLeavesUpperUntouched(t *testing.T) {
	upper, staged := t.TempDir(), t.TempDir()
	file := filepath.Join(upper, "existing")
	if err := os.WriteFile(file, []byte("PVC data"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyRootfs(ctx, staged, upper); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled copy: %v", err)
	}
	if err := publishMaterializedRootfs(staged, upper, filepath.Join(upper, "missing", "marker")); err == nil {
		t.Fatal("expected marker write failure")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "PVC data" {
		t.Fatalf("failed operation changed upper: %q, %v", data, err)
	}
}
