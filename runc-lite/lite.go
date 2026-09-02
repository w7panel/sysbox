package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	liteVolumeInitAnnotation = "sysbox/volume-init"
	liteRootfsAnnotation     = "sysbox/rootfs-rw-layer"
	liteContainerName        = "io.kubernetes.cri.container-name"
	liteHandoffDir           = "/run/sysbox/rootfs-pvc-handoff"
)

type liteVolumeInit struct{ Name, VolumeName, MountPath string }
type liteRootfsEntry struct {
	Name, VolumeName, Path  string
	PersistentSpecialMounts bool     `json:"persistentSpecialMounts"`
	SpecialPath             []string `json:"specialPath"`
}
type liteHandoff struct{ SnapshotKey, PodUID, ContainerName, VolumeName, PVCMountPath string }

func prepareLiteSpec(spec *specs.Spec, id string) error {
	if err := initLiteVolumes(spec); err != nil {
		return err
	}
	return addLiteSpecialMounts(spec, id)
}

func initLiteVolumes(spec *specs.Spec) error {
	raw := spec.Annotations[liteVolumeInitAnnotation]
	if raw == "" || spec.Root == nil {
		return nil
	}
	var entries []liteVolumeInit
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return fmt.Errorf("decode %s: %w", liteVolumeInitAnnotation, err)
	}
	name := spec.Annotations[liteContainerName]
	for _, entry := range entries {
		if entry.Name != name {
			continue
		}
		for _, mount := range spec.Mounts {
			if filepath.Clean(mount.Destination) != filepath.Clean(entry.MountPath) || mount.Type != "bind" {
				continue
			}
			info, err := os.Stat(mount.Source)
			if err != nil || !info.IsDir() {
				continue
			}
			empty, err := dirEmpty(mount.Source)
			if err != nil || !empty {
				continue
			}
			imagePath := filepath.Join(spec.Root.Path, filepath.Clean(entry.MountPath))
			if err := copyDir(imagePath, mount.Source); err != nil {
				return fmt.Errorf("initialize PVC %s: %w", entry.VolumeName, err)
			}
		}
	}
	return nil
}

func addLiteSpecialMounts(spec *specs.Spec, id string) error {
	raw := spec.Annotations[liteRootfsAnnotation]
	if raw == "" {
		return nil
	}
	var entries []liteRootfsEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return fmt.Errorf("decode %s: %w", liteRootfsAnnotation, err)
	}
	name := spec.Annotations[liteContainerName]
	for _, entry := range entries {
		if entry.Name != name || !entry.PersistentSpecialMounts {
			continue
		}
		handoff, err := loadLiteHandoff(id)
		if err != nil {
			return err
		}
		if handoff.ContainerName != name || handoff.VolumeName != entry.VolumeName || handoff.SnapshotKey != id {
			return fmt.Errorf("persistent special handoff does not match container")
		}
		root := filepath.Join(handoff.PVCMountPath, filepath.Clean(entry.Path), "special")
		paths := []string{"/var/lib/docker", "/var/lib/kubelet", "/var/lib/rancher/k3s", "/var/lib/rancher/rke2", "/var/lib/buildkit", "/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs"}
		paths = append(paths, entry.SpecialPath...)
		for _, dest := range paths {
			src := filepath.Join(root, strings.TrimPrefix(filepath.Clean(dest), "/"))
			if err := os.MkdirAll(src, 0755); err != nil {
				return err
			}
			empty, err := dirEmpty(src)
			if err != nil {
				return err
			}
			if empty {
				if err := copyDir(filepath.Join(spec.Root.Path, filepath.Clean(dest)), src); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("initialize special mount %s: %w", dest, err)
				}
			}
			spec.Mounts = append(spec.Mounts, specs.Mount{Source: src, Destination: dest, Type: "bind", Options: []string{"rbind", "rprivate"}})
		}
	}
	return nil
}

func loadLiteHandoff(id string) (liteHandoff, error) {
	sum := sha256.Sum256([]byte(id))
	path := filepath.Join(liteHandoffDir, hex.EncodeToString(sum[:])+".json")
	f, err := os.Open(path)
	if err != nil {
		return liteHandoff{}, fmt.Errorf("open persistent special handoff: %w", err)
	}
	defer f.Close()
	var h liteHandoff
	if err := json.NewDecoder(f).Decode(&h); err != nil {
		return h, err
	}
	return h, nil
}

func dirEmpty(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(32)
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		for _, n := range names {
			if n != "lost+found" {
				return false, nil
			}
		}
	}
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Chmod(target, info.Mode().Perm())
	})
}
