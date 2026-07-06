package rootfs

import (
	"errors"
	"testing"
)

func TestRootfsRwLayerRequestFromLabels_requiresPodUID(t *testing.T) {
	// Given: CRI labels without the required Pod UID.
	labels := map[string]string{
		"io.kubernetes.pod.namespace":  "default",
		"io.kubernetes.pod.name":       "app-0",
		"io.kubernetes.container.name": "my-container",
	}

	// When: the snapshotter parses the identity boundary.
	_, err := rootfsRwLayerRequestFromLabels("snapshot-key", labels)

	// Then: the metadata contract violation is explicit.
	if !errors.Is(err, ErrContainerIdentityIncomplete) {
		t.Fatalf("expected ErrContainerIdentityIncomplete, got %v", err)
	}
}

func TestRootfsRwLayerRequestFromLabels_requiresContainerName(t *testing.T) {
	// Given: CRI labels without the required container name.
	labels := map[string]string{
		"io.kubernetes.pod.namespace": "default",
		"io.kubernetes.pod.name":      "app-0",
		"io.kubernetes.pod.uid":       "pod-uid",
	}

	// When: the snapshotter parses the identity boundary.
	_, err := rootfsRwLayerRequestFromLabels("snapshot-key", labels)

	// Then: sandbox snapshots without a workload container name pass through.
	if !errors.Is(err, ErrContainerIdentityUnavailable) {
		t.Fatalf("expected ErrContainerIdentityUnavailable, got %v", err)
	}
}

func TestRootfsRwLayerRequestFromLabels_preservesRequiredIdentity(t *testing.T) {
	// Given: complete CRI labels for a Kubernetes container.
	labels := map[string]string{
		"io.kubernetes.pod.namespace":  "default",
		"io.kubernetes.pod.name":       "app-0",
		"io.kubernetes.pod.uid":        "pod-uid",
		"io.kubernetes.container.name": "my-container",
	}

	// When: the snapshotter parses the identity boundary.
	request, err := rootfsRwLayerRequestFromLabels("snapshot-key", labels)

	// Then: the strong identity fields are available to downstream resolvers.
	if err != nil {
		t.Fatalf("expected identity parse to succeed, got %v", err)
	}
	if request.PodUID != "pod-uid" {
		t.Fatalf("expected PodUID %q, got %q", "pod-uid", request.PodUID)
	}
	if request.ContainerName != "my-container" {
		t.Fatalf("expected ContainerName %q, got %q", "my-container", request.ContainerName)
	}
}
