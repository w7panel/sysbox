package rootfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type KubeletPVCMountPathResolver struct {
	kubeletPodsPath string
}

func NewKubeletPVCMountPathResolver(kubeletPodsPath string) *KubeletPVCMountPathResolver {
	return &KubeletPVCMountPathResolver{kubeletPodsPath: kubeletPodsPath}
}

func (r *KubeletPVCMountPathResolver) ResolvePVCMountPath(_ context.Context, request RootfsRwLayerRequest, spec RootfsRwLayerSpec) (string, error) {
	if spec.PVCMountPath != "" {
		return spec.PVCMountPath, nil
	}
	if request.PodUID == "" {
		return "", fmt.Errorf("pod uid is required to resolve pvc mount path")
	}
	if spec.PVCClaimName == "" {
		return "", fmt.Errorf("pvc claim name is required to resolve pvc mount path")
	}
	globPattern := filepath.Join(r.kubeletPodsPath, request.PodUID, "volumes", "*", "*")
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no kubelet pvc mount path found for pod uid %s pvc %s", request.PodUID, spec.PVCClaimName)
	}
	candidates := []string{}
	for _, candidate := range matches {
		if strings.Contains(candidate, "kubernetes.io~projected") {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf("multiple kubelet pvc mount path candidates found for pod uid %s pvc %s", request.PodUID, spec.PVCClaimName)
	}
	return "", fmt.Errorf("kubelet pvc mount path not accessible for pod uid %s pvc %s", request.PodUID, spec.PVCClaimName)
}
