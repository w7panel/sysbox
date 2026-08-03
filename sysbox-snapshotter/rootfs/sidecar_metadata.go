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
	intent, err := parsePodAnnotationIntent(request.RootfsRwLayerAnnotation)
	if err != nil {
		return RootfsRwLayerSpec{}, err
	}
	if request.ContainerName == SidecarContainerName {
		if len(intent.Entries) == 0 {
			return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
		}
		// The sidecar's own snapshot is prepared before its container metadata
		// is guaranteed to exist in containerd. It never uses the persistent
		// rootfs layer, so do not introduce a self-referential spec lookup.
		return RootfsRwLayerSpec{Sidecar: true}, nil
	}
	for _, entry := range intent.Entries {
		if entry.ContainerName != request.ContainerName {
			continue
		}
		if _, err := safeLayerPath(entry.Path); err != nil {
			return RootfsRwLayerSpec{}, err
		}
		sidecarSpec, err := r.store.LoadSidecarSpec(ctx, request)
		if err != nil {
			if errors.Is(err, ErrSidecarSpecUnavailable) {
				return RootfsRwLayerSpec{}, err
			}
			return RootfsRwLayerSpec{}, err
		}
		return RootfsRwLayerSpec{
			VolumeName:              entry.VolumeName,
			Path:                    entry.Path,
			PersistentSpecialMounts: entry.PersistentSpecialMounts,
			SpecialPath:             append([]string(nil), entry.SpecialPath...),
			sidecarSpec:             sidecarSpec,
		}, nil
	}
	return RootfsRwLayerSpec{}, ErrRootfsRwLayerNotConfigured
}

func parsePodAnnotationIntent(raw string) (Intent, error) {
	var entries []struct {
		Name                    string   `json:"name"`
		VolumeName              string   `json:"volumeName"`
		Path                    string   `json:"path"`
		PersistentSpecialMounts bool     `json:"persistentSpecialMounts,omitempty"`
		SpecialPath             []string `json:"specialPath,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return Intent{}, fmt.Errorf("decode rootfs rw-layer annotation: %w", err)
	}
	intent := Intent{Entries: make([]IntentEntry, 0, len(entries))}
	for _, entry := range entries {
		intent.Entries = append(intent.Entries, IntentEntry{
			ContainerName:           entry.Name,
			VolumeName:              entry.VolumeName,
			Path:                    entry.Path,
			PersistentSpecialMounts: entry.PersistentSpecialMounts,
			SpecialPath:             entry.SpecialPath,
		})
	}
	return intent, nil
}
