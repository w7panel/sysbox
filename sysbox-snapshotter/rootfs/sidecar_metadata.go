package rootfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type SidecarMetadataResolver struct{ store SidecarSpecStore }

func NewSidecarMetadataResolver(store SidecarSpecStore) *SidecarMetadataResolver {
	return &SidecarMetadataResolver{store: store}
}

func (r *SidecarMetadataResolver) ResolveRootfsRwLayer(ctx context.Context, request RootfsRwLayerRequest) (RootfsRwLayerSpec, error) {
	if request.ContainerName == "" {
		return RootfsRwLayerSpec{}, fmt.Errorf("missing container name in rootfs rw-layer request: %w", ErrContainerIdentityIncomplete)
	}
	if request.RootfsRwLayerAnnotation == "" {
		return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
	}
	sidecarSpec, err := r.store.LoadSidecarSpec(ctx, request)
	if err != nil {
		if errors.Is(err, ErrSidecarSpecUnavailable) {
			return RootfsRwLayerSpec{}, err
		}
		return RootfsRwLayerSpec{}, err
	}
	intent, err := parsePodAnnotationIntent(request.RootfsRwLayerAnnotation)
	if err != nil {
		return RootfsRwLayerSpec{}, err
	}
	if request.ContainerName == SidecarContainerName {
		if len(intent.Entries) == 0 {
			return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
		}
		return RootfsRwLayerSpec{Sidecar: true, sidecarSpec: sidecarSpec}, nil
	}
	for _, entry := range intent.Entries {
		if entry.ContainerName != request.ContainerName {
			continue
		}
		if _, err := safeLayerPath(entry.Path); err != nil {
			return RootfsRwLayerSpec{}, err
		}
		return RootfsRwLayerSpec{VolumeName: entry.VolumeName, Path: entry.Path, sidecarSpec: sidecarSpec}, nil
	}
	return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
}

func parsePodAnnotationIntent(raw string) (Intent, error) {
	var entries []struct {
		Name       string `json:"name"`
		VolumeName string `json:"volumeName"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return Intent{}, fmt.Errorf("decode rootfs rw-layer annotation: %w", err)
	}
	intent := Intent{Entries: make([]IntentEntry, 0, len(entries))}
	for _, entry := range entries {
		intent.Entries = append(intent.Entries, IntentEntry{ContainerName: entry.Name, VolumeName: entry.VolumeName, Path: entry.Path})
	}
	return intent, nil
}
