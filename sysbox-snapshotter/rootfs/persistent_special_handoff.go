package rootfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	PersistentSpecialHandoffVersion = 1
	PersistentSpecialHandoffDir     = "/run/sysbox/rootfs-pvc-handoff"
)

type FilePersistentSpecialHandoffStore struct{ root string }

func NewFilePersistentSpecialHandoffStore(root string) *FilePersistentSpecialHandoffStore {
	return &FilePersistentSpecialHandoffStore{root: root}
}

func (s *FilePersistentSpecialHandoffStore) Write(ctx context.Context, handoff PersistentSpecialHandoff) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if handoff.Version != PersistentSpecialHandoffVersion || handoff.SnapshotKey == "" || handoff.PodUID == "" || handoff.ContainerName == "" || handoff.VolumeName == "" || handoff.PVCMountPath == "" {
		return fmt.Errorf("persistent special handoff is incomplete")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create persistent special handoff directory: %w", err)
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return fmt.Errorf("chmod persistent special handoff directory: %w", err)
	}
	data, err := json.Marshal(handoff)
	if err != nil {
		return fmt.Errorf("encode persistent special handoff: %w", err)
	}
	tmp, err := os.CreateTemp(s.root, ".handoff-")
	if err != nil {
		return fmt.Errorf("create persistent special handoff: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod persistent special handoff: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write persistent special handoff: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close persistent special handoff: %w", err)
	}
	if err := os.Rename(tmpName, s.path(handoff.SnapshotKey)); err != nil {
		return fmt.Errorf("commit persistent special handoff: %w", err)
	}
	return nil
}

func (s *FilePersistentSpecialHandoffStore) Remove(ctx context.Context, snapshotKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshotKey == "" {
		return nil
	}
	if err := os.Remove(s.path(snapshotKey)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove persistent special handoff: %w", err)
	}
	return nil
}

func (s *FilePersistentSpecialHandoffStore) path(snapshotKey string) string {
	sum := sha256.Sum256([]byte(snapshotKey))
	return filepath.Join(s.root, hex.EncodeToString(sum[:])+".json")
}
