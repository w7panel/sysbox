package rootfs

import (
	"errors"
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestSidecarContainerFilters_usesPodUID_whenAvailable(t *testing.T) {
	// Given: a request resolved from the target business container.
	request := RootfsRwLayerRequest{
		Namespace: "default",
		PodName:   "app-0",
		PodUID:    "current-pod-uid",
	}

	// When: the snapshotter builds the containerd query for the sidecar.
	filters := sidecarContainerFilters(request)

	// Then: one containerd filter expresses AND semantics for Pod UID and sidecar name.
	expected := []string{
		`labels."io.kubernetes.pod.uid"==current-pod-uid,labels."io.kubernetes.container.name"==sysbox-rootfs`,
	}
	if len(filters) != len(expected) {
		t.Fatalf("expected %d filters, got %d: %v", len(expected), len(filters), filters)
	}
	for i := range expected {
		if filters[i] != expected[i] {
			t.Fatalf("filter %d: expected %q, got %q", i, expected[i], filters[i])
		}
	}
}

func TestSidecarContainerFilters_returnsNoFilters_whenPodUIDMissing(t *testing.T) {
	// Given: a request without required Pod UID metadata.
	request := RootfsRwLayerRequest{
		Namespace: "default",
		PodName:   "app-0",
	}

	// When: the snapshotter builds the containerd query for the sidecar.
	filters := sidecarContainerFilters(request)

	// Then: no weak namespace/name fallback filters are produced.
	if len(filters) != 0 {
		t.Fatalf("expected no filters without Pod UID, got %v", filters)
	}
}

func TestSidecarMatchesRequest_rejectsSidecar_whenPodUIDMissing(t *testing.T) {
	// Given: labels that would match a weak namespace/name lookup.
	labels := map[string]string{
		"io.kubernetes.pod.namespace":  "default",
		"io.kubernetes.pod.name":       "app-0",
		"io.kubernetes.pod.uid":        "current-pod-uid",
		"io.kubernetes.container.name": SidecarContainerName,
	}
	request := RootfsRwLayerRequest{
		Namespace: "default",
		PodName:   "app-0",
	}

	// When: the snapshotter evaluates sidecar ownership.
	matches := sidecarMatchesRequest(labels, request)

	// Then: missing Pod UID prevents sidecar selection.
	if matches {
		t.Fatal("expected sidecar selection to require Pod UID")
	}
}

func TestSidecarMatchesRequest_rejectsStaleSidecar_whenPodUIDDiffers(t *testing.T) {
	// Given: a sidecar from a previous Pod instance with the same namespace/name.
	labels := map[string]string{
		"io.kubernetes.pod.namespace":  "default",
		"io.kubernetes.pod.name":       "app-0",
		"io.kubernetes.pod.uid":        "old-pod-uid",
		"io.kubernetes.container.name": SidecarContainerName,
	}
	request := RootfsRwLayerRequest{
		Namespace: "default",
		PodName:   "app-0",
		PodUID:    "new-pod-uid",
	}

	// When: the snapshotter evaluates whether that sidecar belongs to this request.
	matches := sidecarMatchesRequest(labels, request)

	// Then: the stale same-name sidecar is not accepted.
	if matches {
		t.Fatal("expected sidecar with a different Pod UID to be rejected")
	}
}

func TestSidecarMatchesRequest_acceptsSidecar_whenPodUIDMatches(t *testing.T) {
	// Given: a sidecar from the current Pod instance.
	labels := map[string]string{
		"io.kubernetes.pod.namespace":  "default",
		"io.kubernetes.pod.name":       "app-0",
		"io.kubernetes.pod.uid":        "current-pod-uid",
		"io.kubernetes.container.name": SidecarContainerName,
	}
	request := RootfsRwLayerRequest{
		Namespace: "default",
		PodName:   "app-0",
		PodUID:    "current-pod-uid",
	}

	// When: the snapshotter evaluates whether that sidecar belongs to this request.
	matches := sidecarMatchesRequest(labels, request)

	// Then: the current Pod sidecar is accepted.
	if !matches {
		t.Fatal("expected sidecar with matching Pod UID to be accepted")
	}
}

func TestUniqueSidecarSpec_returnsAmbiguous_whenMultipleSidecarsMatch(t *testing.T) {
	// Given: containerd returned more than one sidecar for the same immutable Pod UID.
	specs := []*runtimespec.Spec{
		{Hostname: "sidecar-a"},
		{Hostname: "sidecar-b"},
	}

	// When: the snapshotter selects the sidecar spec for the rootfs request.
	_, err := uniqueSidecarSpec(specs)

	// Then: ambiguous ownership is surfaced instead of choosing the first candidate.
	if !errors.Is(err, ErrSidecarSpecAmbiguous) {
		t.Fatalf("expected ambiguous sidecar spec error, got %v", err)
	}
}
