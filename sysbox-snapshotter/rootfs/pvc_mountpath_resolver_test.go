package rootfs

import "testing"

func TestStablePVCMountPathUsesSysboxBindRoot(t *testing.T) {
	source := "/var/lib/kubelet/pods/new-pod/volumes/kubernetes.io~local-volume/pvc-uid"
	mountinfo := "1929 4256 8:16 /rootfs/special/var/lib/rancher/k3s/storage/pvc-uid_default_rootfs " + source + " rw,relatime,idmapped - ext4 /dev/longhorn/pvc rw\n"

	got, err := stablePVCMountPathFromMountInfo(source, func() ([]byte, error) { return []byte(mountinfo), nil })
	if err != nil {
		t.Fatalf("resolve stable PVC mount path: %v", err)
	}
	want := "/var/lib/rancher/k3s/storage/pvc-uid_default_rootfs"
	if got != want {
		t.Fatalf("stable path = %q, want %q", got, want)
	}
}

func TestStablePVCMountPathKeepsNonSysboxMount(t *testing.T) {
	source := "/var/lib/kubelet/pods/new-pod/volumes/kubernetes.io~csi/pvc/mount"
	mountinfo := "35 24 0:32 / " + source + " rw,relatime - tmpfs tmpfs rw\n"

	got, err := stablePVCMountPathFromMountInfo(source, func() ([]byte, error) { return []byte(mountinfo), nil })
	if err != nil {
		t.Fatalf("resolve non-Sysbox PVC mount path: %v", err)
	}
	if got != source {
		t.Fatalf("path = %q, want original %q", got, source)
	}
}

func TestUnescapeMountInfoPath(t *testing.T) {
	if got, want := unescapeMountInfoPath(`/var/lib/pvc\040with\040spaces`), "/var/lib/pvc with spaces"; got != want {
		t.Fatalf("unescaped path = %q, want %q", got, want)
	}
}
