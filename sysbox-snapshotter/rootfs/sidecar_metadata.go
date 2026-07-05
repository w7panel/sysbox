package rootfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

type SidecarMetadataResolver struct {
	store SidecarSpecStore
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
	prefix := SpecEnv + "="
	for _, env := range spec.Process.Env {
		if value, ok := strings.CutPrefix(env, prefix); ok {
			return value, true
		}
	}
	return "", false
}

func parseSidecarIntent(raw string) (Intent, error) {
	var intent Intent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return Intent{}, fmt.Errorf("decode sidecar rootfs rw-layer intent: %w", err)
	}
	if intent.Version != 1 {
		return Intent{}, fmt.Errorf("unsupported sidecar rootfs rw-layer intent version %d", intent.Version)
	}
	return intent, nil
}
