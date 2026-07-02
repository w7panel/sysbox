package rootfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const RootfsRwLayerSpecEnv = "ROOTFS_RW_LAYER_SPEC"

type SidecarMetadataResolver struct {
	store SidecarSpecStore
}

type sidecarIntent struct {
	Version int                  `json:"version"`
	Entries []sidecarIntentEntry `json:"entries"`
}

type sidecarIntentEntry struct {
	ContainerName string `json:"containerName"`
	VolumeName    string `json:"volumeName"`
	Path          string `json:"path"`
	PVCClaimName  string `json:"pvcClaimName"`
}

func NewSidecarMetadataResolver(store SidecarSpecStore) *SidecarMetadataResolver {
	return &SidecarMetadataResolver{store: store}
}

func (r *SidecarMetadataResolver) ResolveRootfsRwLayer(
	ctx context.Context,
	request RootfsRwLayerRequest,
) (RootfsRwLayerSpec, error) {
	if request.ContainerName == "" {
		return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
	}
	sidecarSpec, err := r.store.LoadSidecarSpec(ctx, request)
	if err != nil {
		if errors.Is(err, ErrSidecarSpecUnavailable) {
			return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
		}
		return RootfsRwLayerSpec{}, err
	}
	raw, found := rootfsIntentEnv(sidecarSpec)
	if !found {
		return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
	}
	intent, err := parseSidecarIntent(raw)
	if err != nil {
		return RootfsRwLayerSpec{}, err
	}
	for _, entry := range intent.Entries {
		if entry.ContainerName != request.ContainerName {
			continue
		}
		if _, err := safeLayerPath(entry.Path); err != nil {
			return RootfsRwLayerSpec{}, err
		}
		return RootfsRwLayerSpec{
			Namespace:    request.Namespace,
			PodName:      request.PodName,
			VolumeName:   entry.VolumeName,
			Path:         entry.Path,
			PVCClaimName: entry.PVCClaimName,
		}, nil
	}
	return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
}

func rootfsIntentEnv(spec *runtimespec.Spec) (string, bool) {
	if spec == nil || spec.Process == nil {
		return "", false
	}
	prefix := RootfsRwLayerSpecEnv + "="
	for _, env := range spec.Process.Env {
		if strings.HasPrefix(env, prefix) {
			return strings.TrimPrefix(env, prefix), true
		}
	}
	return "", false
}

func parseSidecarIntent(raw string) (sidecarIntent, error) {
	var intent sidecarIntent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return sidecarIntent{}, fmt.Errorf("decode sidecar rootfs rw-layer intent: %w", err)
	}
	if intent.Version != 1 {
		return sidecarIntent{}, fmt.Errorf("unsupported sidecar rootfs rw-layer intent version %d", intent.Version)
	}
	return intent, nil
}
