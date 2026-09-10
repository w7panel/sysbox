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
	liteRootfsAnnotation = "sysbox/rootfs-rw-layer"
	liteContainerName    = "io.kubernetes.cri.container-name"
	liteSandboxUID       = "io.kubernetes.cri.sandbox-uid"
	liteHandoffDir       = "/run/sysbox/rootfs-pvc-handoff"
	liteKubeletPodsDir   = "/var/lib/kubelet/pods"
)

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
	return initLiteVolumesAt(spec, liteKubeletPodsDir)
}

func initLiteVolumesAt(spec *specs.Spec, podsDir string) error {
	if spec.Root == nil || spec.Root.Path == "" {
		return nil
	}
	name := spec.Annotations[liteContainerName]
	podUID := spec.Annotations[liteSandboxUID]
	if name == "" || podUID == "" {
		return nil
	}
	rootfs, err := filepath.Abs(spec.Root.Path)
	if err != nil {
		return fmt.Errorf("resolve container rootfs: %w", err)
	}
	for _, mount := range spec.Mounts {
		if mount.Type != "bind" || liteMountReadOnly(mount) {
			continue
		}
		source, ok := detectLitePVCSourceAt(mount.Source, podUID, name, podsDir)
		if !ok {
			continue
		}
		info, err := os.Lstat(source)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		empty, err := dirEmpty(source)
		if err != nil || !empty {
			continue
		}
		relative := strings.TrimPrefix(filepath.Clean(mount.Destination), string(filepath.Separator))
		if relative == "." || relative == "" {
			continue
		}
		imagePath := filepath.Join(rootfs, relative)
		info, err = os.Lstat(imagePath)
		if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect image volume path %s: %w", mount.Destination, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("image volume path %s is a symlink", mount.Destination)
		}
		if err := copyDir(imagePath, source); err != nil {
			return fmt.Errorf("initialize PVC mount %s: %w", mount.Destination, err)
		}
	}
	return nil
}

func liteMountReadOnly(mount specs.Mount) bool {
	for _, option := range mount.Options {
		if option == "ro" {
			return true
		}
	}
	return false
}

// detectLitePVCSource accepts only CSI kubelet paths for the current Pod.
// It intentionally skips emptyDir, projected volumes, hostPath, and unknown
// storage plugins because runc-lite has no Kubernetes API client.
func detectLitePVCSource(source, podUID, containerName string) (string, bool) {
	return detectLitePVCSourceAt(source, podUID, containerName, liteKubeletPodsDir)
}

func detectLitePVCSourceAt(source, podUID, containerName, podsDir string) (string, bool) {
	cleanSource, err := filepath.Abs(source)
	if err != nil {
		return "", false
	}
	podRoot := filepath.Join(podsDir, podUID)
	volumeRoots := []string{
		filepath.Join(podRoot, "volumes", "kubernetes.io~csi"),
		// k3s local-path exposes dynamically provisioned PVCs through the
		// in-tree local-volume path. It has the same per-Pod, kubelet-owned
		// layout as CSI and is safe to initialize without an API client.
		filepath.Join(podRoot, "volumes", "kubernetes.io~local-volume"),
	}
	for _, directRoot := range volumeRoots {
		if rel, err := filepath.Rel(directRoot, cleanSource); err == nil {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) == 1 && parts[0] != "" {
				return cleanSource, true
			}
			if len(parts) == 2 && parts[0] != "" && parts[1] == "mount" {
				return cleanSource, true
			}
		}
	}
	subpathRoot := filepath.Join(podRoot, "volume-subpaths")
	if rel, err := filepath.Rel(subpathRoot, cleanSource); err == nil {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return "", false
		}
		containerMatches := parts[1] == containerName || strings.HasPrefix(containerName, parts[1]+"-")
		volumeMatches := strings.HasPrefix(parts[0], "pvc-")
		if !volumeMatches {
			for _, directRoot := range volumeRoots {
				if _, err = os.Stat(filepath.Join(directRoot, parts[0], "mount")); err == nil {
					volumeMatches = true
					break
				}
				if _, err = os.Stat(filepath.Join(directRoot, parts[0])); err == nil {
					volumeMatches = true
					break
				}
			}
		}
		if containerMatches && volumeMatches {
			return cleanSource, true
		}
	}
	return "", false
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
