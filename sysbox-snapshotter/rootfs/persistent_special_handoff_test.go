package rootfs

import (
	"context"
	"os"
	"testing"
)

func TestFilePersistentSpecialHandoffStoreWritesAndRemoves(t *testing.T) {
	store := NewFilePersistentSpecialHandoffStore(t.TempDir())
	handoff := PersistentSpecialHandoff{
		Version:       PersistentSpecialHandoffVersion,
		SnapshotKey:   "container-id",
		PodUID:        "pod-uid",
		ContainerName: "app",
		VolumeName:    "rootfs",
		PVCMountPath:  "/var/lib/kubelet/pods/pod-uid/volumes/kubernetes.io~csi/pvc/mount",
	}
	if err := store.Write(context.Background(), handoff); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.path(handoff.SnapshotKey))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("handoff mode = %o, want 600", got)
	}
	if err := store.Remove(context.Background(), handoff.SnapshotKey); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.path(handoff.SnapshotKey)); !os.IsNotExist(err) {
		t.Fatalf("handoff still exists: %v", err)
	}
}

func TestFilePersistentSpecialHandoffStoreRejectsIncompleteRecord(t *testing.T) {
	store := NewFilePersistentSpecialHandoffStore(t.TempDir())
	if err := store.Write(context.Background(), PersistentSpecialHandoff{Version: PersistentSpecialHandoffVersion}); err == nil {
		t.Fatal("expected incomplete handoff error")
	}
}
