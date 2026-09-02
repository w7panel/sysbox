package rootfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

var (
	ErrSidecarSpecUnavailable = errors.New("sysbox sidecar oci spec unavailable")
	ErrSidecarSpecAmbiguous   = errors.New("sysbox sidecar oci spec ambiguous")
	ErrSidecarSpecMalformed   = errors.New("sysbox sidecar oci spec malformed")
	ErrPVCMountNotFound       = errors.New("sysbox sidecar pvc mount not found")
)

type SidecarSpecStore interface {
	LoadSidecarSpec(ctx context.Context, request RootfsRwLayerRequest) (*runtimespec.Spec, error)
}

type PVCMountPathResolverFromSidecar struct{ store SidecarSpecStore }

func NewPVCMountPathResolver(store SidecarSpecStore) *PVCMountPathResolverFromSidecar {
	return &PVCMountPathResolverFromSidecar{store: store}
}

func (r *PVCMountPathResolverFromSidecar) ResolvePVCMountPath(ctx context.Context, request RootfsRwLayerRequest, spec RootfsRwLayerSpec) (string, error) {
	if spec.VolumeName == "" {
		return "", fmt.Errorf("volume name is required to resolve sidecar pvc mount path: %w", ErrSidecarSpecMalformed)
	}
	sidecarSpec := spec.sidecarSpec
	if sidecarSpec == nil {
		loaded, err := r.store.LoadSidecarSpec(ctx, request)
		if err != nil {
			return "", err
		}
		sidecarSpec = loaded
	}
	if sidecarSpec == nil {
		return "", ErrSidecarSpecUnavailable
	}
	target := filepath.ToSlash(filepath.Join(SidecarMountPath, spec.VolumeName))
	for _, mount := range sidecarSpec.Mounts {
		if cleanOCIDestination(mount.Destination) != target {
			continue
		}
		if mount.Source == "" {
			return "", ErrSidecarSpecMalformed
		}
		return stablePVCMountPath(mount.Source)
	}
	return "", ErrPVCMountNotFound
}

// stablePVCMountPath resolves the L1-visible source of the kubelet bind mount.
//
// The OCI sidecar spec deliberately supplies the kubelet pod-volume mount as
// the trusted PVC identity. That path contains the Pod UID, so keeping it as
// the overlay upperdir makes a recreated Pod start with a fresh directory.
// In a Sysbox L1, mountinfo preserves the original bind source in the mount
// root field. Sysbox prefixes L0 special mounts with /rootfs/special there;
// removing that namespace-only prefix gives the stable L1-visible PVC path.
// If this is not a Sysbox special bind, retain the OCI source unchanged.
func stablePVCMountPath(source string) (string, error) {
	return stablePVCMountPathFromMountInfo(source, func() ([]byte, error) {
		return os.ReadFile("/proc/self/mountinfo")
	})
}

func stablePVCMountPathFromMountInfo(source string, readMountInfo func() ([]byte, error)) (string, error) {
	data, err := readMountInfo()
	if err != nil {
		return "", fmt.Errorf("read mountinfo: %w", err)
	}
	source = filepath.Clean(source)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || filepath.Clean(unescapeMountInfoPath(fields[4])) != source {
			continue
		}
		root := filepath.Clean(unescapeMountInfoPath(fields[3]))
		const sysboxSpecialRoot = "/rootfs/special"
		if root == sysboxSpecialRoot || !strings.HasPrefix(root, sysboxSpecialRoot+"/") {
			return source, nil
		}
		return strings.TrimPrefix(root, sysboxSpecialRoot), nil
	}
	return source, nil
}

func unescapeMountInfoPath(value string) string {
	return strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	).Replace(value)
}

func cleanOCIDestination(destination string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(destination)))
}
