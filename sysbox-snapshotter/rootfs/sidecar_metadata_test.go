package rootfs

import (
	"context"
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestSidecarMetadataResolver_resolvesContainerFromPodAnnotation(t *testing.T) {
	resolver := NewSidecarMetadataResolver(fakeSidecarStore{spec: &runtimespec.Spec{}})
	request := RootfsRwLayerRequest{
		ContainerName:           "app",
		RootfsRwLayerAnnotation: `[{"name":"app","volumeName":"rootfs","path":"containers/app"}]`,
	}

	spec, err := resolver.ResolveRootfsRwLayer(context.Background(), request)

	if err != nil {
		t.Fatal(err)
	}
	if spec.VolumeName != "rootfs" || spec.Path != "containers/app" || spec.Sidecar {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestSidecarMetadataResolver_resolvesSidecarFromPodAnnotation(t *testing.T) {
	resolver := NewSidecarMetadataResolver(fakeSidecarStore{err: ErrSidecarSpecUnavailable})
	request := RootfsRwLayerRequest{
		ContainerName:           SidecarContainerName,
		RootfsRwLayerAnnotation: `[{"name":"app","volumeName":"rootfs","path":"containers/app"}]`,
	}

	spec, err := resolver.ResolveRootfsRwLayer(context.Background(), request)

	if err != nil {
		t.Fatal(err)
	}
	if !spec.Sidecar {
		t.Fatalf("spec.Sidecar = false")
	}
}

type fakeSidecarStore struct {
	spec *runtimespec.Spec
	err  error
}

func (f fakeSidecarStore) LoadSidecarSpec(context.Context, RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	return f.spec, f.err
}
