package rootfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type LocalPreparer struct{}

type layerMetadata struct {
	Version        int         `json:"version"`
	State          string      `json:"state"`
	SnapshotKey    string      `json:"snapshotKey"`
	PodUID         string      `json:"podUID"`
	ContainerName  string      `json:"containerName"`
	VolumeName     string      `json:"volumeName"`
	Path           string      `json:"path"`
	PVCClaimName   string      `json:"pvcClaimName"`
	ImageChainID   string      `json:"imageChainID"`
	UIDMappings    []IDMapping `json:"uidMappings"`
	GIDMappings    []IDMapping `json:"gidMappings"`
	CreatedAt      time.Time   `json:"createdAt"`
	LastAttachedAt time.Time   `json:"lastAttachedAt"`
}

func NewLocalPreparer() *LocalPreparer {
	return &LocalPreparer{}
}

func (p *LocalPreparer) PrepareRootfsRwLayer(
	ctx context.Context,
	request PrepareRootfsRequest,
) (PreparedRootfs, error) {
	if err := ctx.Err(); err != nil {
		return PreparedRootfs{}, err
	}
	if request.PVCMountPath == "" {
		return PreparedRootfs{}, fmt.Errorf("pvc mount path is required")
	}
	layerRoot, err := safeJoin(request.PVCMountPath, request.Path)
	if err != nil {
		return PreparedRootfs{}, err
	}
	if err := rejectSymlink(layerRoot); err != nil {
		return PreparedRootfs{}, err
	}
	metaPath := filepath.Join(layerRoot, "meta.json")
	if _, err := os.Stat(metaPath); errors.Is(err, os.ErrNotExist) {
		if err := p.initializeLayer(layerRoot, metaPath, request); err != nil {
			return PreparedRootfs{}, err
		}
	} else if err != nil {
		return PreparedRootfs{}, fmt.Errorf("stat rootfs rw-layer metadata: %w", err)
	} else if err := p.ensureManagedLayer(layerRoot, metaPath, request); err != nil {
		return PreparedRootfs{}, err
	}
	if err := p.attachLayer(metaPath, request); err != nil {
		return PreparedRootfs{}, err
	}

	return PreparedRootfs{
		UpperDir: filepath.Join(layerRoot, "upper"),
		WorkDir:  filepath.Join(layerRoot, "work"),
	}, nil
}

func (p *LocalPreparer) initializeLayer(layerRoot string, metaPath string, request PrepareRootfsRequest) error {
	entries, err := os.ReadDir(layerRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read rootfs rw-layer directory: %w", err)
	}
	if len(entries) > 0 {
		return ErrUnmanagedRootfsLayer
	}
	if err := os.MkdirAll(layerRoot, 0o755); err != nil {
		return fmt.Errorf("create rootfs rw-layer root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(layerRoot, "upper"), 0o755); err != nil {
		return fmt.Errorf("create rootfs upperdir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(layerRoot, "work"), 0o711); err != nil {
		return fmt.Errorf("create rootfs workdir: %w", err)
	}
	if err := p.ensureContainerRootOwnership(layerRoot, request); err != nil {
		return err
	}
	metadata := layerMetadata{
		Version:       1,
		State:         "available",
		SnapshotKey:   request.SnapshotKey,
		PodUID:        request.PodUID,
		ContainerName: request.ContainerName,
		VolumeName:    request.VolumeName,
		Path:          request.Path,
		PVCClaimName:  request.PVCClaimName,
		ImageChainID:  request.ImageChainID,
		UIDMappings:   request.UIDMappings,
		GIDMappings:   request.GIDMappings,
		CreatedAt:     time.Now().UTC(),
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode rootfs rw-layer metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		return fmt.Errorf("write rootfs rw-layer metadata: %w", err)
	}
	return nil
}

func (p *LocalPreparer) attachLayer(metaPath string, request PrepareRootfsRequest) error {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read rootfs rw-layer metadata: %w", err)
	}
	var metadata layerMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("decode rootfs rw-layer metadata: %w", err)
	}
	metadata.State = "attached"
	metadata.SnapshotKey = request.SnapshotKey
	metadata.PodUID = request.PodUID
	metadata.ContainerName = request.ContainerName
	metadata.UIDMappings = request.UIDMappings
	metadata.GIDMappings = request.GIDMappings
	metadata.LastAttachedAt = time.Now().UTC()
	data, err = json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode rootfs rw-layer metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		return fmt.Errorf("write rootfs rw-layer metadata: %w", err)
	}
	return nil
}

func (p *LocalPreparer) ensureManagedLayer(layerRoot string, metaPath string, request PrepareRootfsRequest) error {
	for _, name := range []string{"upper", "work"} {
		info, err := os.Stat(filepath.Join(layerRoot, name))
		if err != nil {
			return fmt.Errorf("stat rootfs %s dir: %w", name, err)
		}
		if !info.IsDir() {
			return ErrUnmanagedRootfsLayer
		}
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read rootfs rw-layer metadata: %w", err)
	}
	var metadata layerMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("decode rootfs rw-layer metadata: %w", err)
	}
	if !metadata.compatibleWith(request) {
		return ErrIncompatibleRootfsLayer
	}
	if err := p.ensureContainerRootOwnership(layerRoot, request); err != nil {
		return err
	}
	return nil
}

func (p *LocalPreparer) ensureContainerRootOwnership(layerRoot string, request PrepareRootfsRequest) error {
	uid, ok := RootHostIdentity(request.UIDMappings)
	if !ok {
		uid = 0
	}
	gid, ok := RootHostIdentity(request.GIDMappings)
	if !ok {
		gid = 0
	}
	if err := filepath.Walk(layerRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if chownErr := os.Chown(path, int(uid), int(gid)); chownErr != nil {
			return fmt.Errorf("chown %s: %w", path, chownErr)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := os.Chmod(layerRoot, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", layerRoot, err)
	}
	if err := os.Chmod(filepath.Join(layerRoot, "upper"), 0o755); err != nil {
		return fmt.Errorf("chmod upper: %w", err)
	}
	if err := os.Chmod(filepath.Join(layerRoot, "work"), 0o711); err != nil {
		return fmt.Errorf("chmod work: %w", err)
	}
	return nil
}

func (m layerMetadata) compatibleWith(request PrepareRootfsRequest) bool {
	if m.Version != 1 {
		return false
	}
	if m.State != "available" && m.State != "attached" {
		return false
	}
	if m.ImageChainID != "" && request.ImageChainID != "" && m.ImageChainID != request.ImageChainID {
		return false
	}
	return true
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat rootfs rw-layer path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafeLayerPath
	}
	return nil
}
