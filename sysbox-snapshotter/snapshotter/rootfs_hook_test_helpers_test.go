//go:build linux

package overlay

import (
	"context"
	"errors"

	"github.com/nestybox/sysbox-snapshotter/rootfs"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

var errRootfsHookTest = errors.New("rootfs hook test error")

type fakeIdentityResolver struct {
	request rootfs.RootfsRwLayerRequest
	err     error
}

func (f fakeIdentityResolver) ResolveIdentity(_ context.Context, _ string) (rootfs.RootfsRwLayerRequest, error) {
	return f.request, f.err
}

type fakeMetadataResolver struct {
	spec rootfs.RootfsRwLayerSpec
	err  error
}

func (f fakeMetadataResolver) ResolveRootfsRwLayer(_ context.Context, _ rootfs.RootfsRwLayerRequest) (rootfs.RootfsRwLayerSpec, error) {
	return f.spec, f.err
}

type fakePVCResolver struct {
	mountPath string
	err       error
}

func (f fakePVCResolver) ResolvePVCMountPath(_ context.Context, _ rootfs.RootfsRwLayerRequest, _ rootfs.RootfsRwLayerSpec) (string, error) {
	return f.mountPath, f.err
}

type fakeRootfsPreparer struct {
	prepared rootfs.PreparedRootfs
	request  rootfs.PrepareRootfsRequest
	err      error
}

func (f *fakeRootfsPreparer) PrepareRootfsRwLayer(_ context.Context, request rootfs.PrepareRootfsRequest) (rootfs.PreparedRootfs, error) {
	f.request = request
	return f.prepared, f.err
}

type countingSidecarSpecStore struct {
	spec  *runtimespec.Spec
	loads int
}

func (s *countingSidecarSpecStore) LoadSidecarSpec(_ context.Context, _ rootfs.RootfsRwLayerRequest) (*runtimespec.Spec, error) {
	s.loads++
	return s.spec, nil
}
