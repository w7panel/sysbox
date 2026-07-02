package main

import (
	"testing"

	overlay "github.com/nestybox/sysbox-snapshotter/snapshotter"
	"github.com/stretchr/testify/require"
)

func TestParseSnapshotterFlags_derivesDefaultRootFromContainerdDataRoot(t *testing.T) {
	// Given
	resolver := func() (string, error) { return "/var/lib/containerd-test", nil }

	// When
	flags, err := parseSnapshotterFlagsWithDataRoot([]string{}, resolver)

	// Then
	require.NoError(t, err)
	require.Equal(t, "/var/lib/containerd-test/io.containerd.snapshotter.v1.sysbox", flags.Root)
}

func TestParseSnapshotterFlags_keepsExplicitRoot(t *testing.T) {
	// Given
	resolver := func() (string, error) { return "/var/lib/containerd-test", nil }

	// When
	flags, err := parseSnapshotterFlagsWithDataRoot([]string{"--root", "/custom/sysbox-root"}, resolver)

	// Then
	require.NoError(t, err)
	require.Equal(t, "/custom/sysbox-root", flags.Root)
}

func TestLoadRuntimeConfig_usesContainerdGRPCAddress(t *testing.T) {
	// Given
	resolvers := runtimeResolvers{
		Capabilities: func(string) ([]string, error) { return nil, nil },
		DataRoot:     func() (string, error) { return "/var/lib/containerd-test", nil },
		GRPCAddress:  func() (string, error) { return "/run/custom/containerd.sock", nil },
	}

	// When
	_, config, err := loadRuntimeConfig([]string{}, resolvers)

	// Then
	require.NoError(t, err)
	require.Equal(t, "/run/custom/containerd.sock", config.ContainerdSocket)
}

func TestBuildSnapshotterOptions_disablesRemapIDs_whenCapabilityMissing(t *testing.T) {
	// Given
	config := snapshotterConfig{
		Capabilities: []string{},
		SupportsIDMappedMounts: func() (bool, error) {
			return true, nil
		},
	}

	// When
	opts, err := buildSnapshotterOptions(config)

	// Then
	require.NoError(t, err)
	snapshotter, err := overlay.NewSnapshotter(t.TempDir(), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshotter.Close()) })
	require.False(t, overlay.HasRemapIDs(snapshotter))
	require.True(t, overlay.HasRootfsHooks(snapshotter))
}

func TestParseSnapshotterFlags_returnsError_whenDeprecatedAliasIsUsed(t *testing.T) {
	resolver := func() (string, error) { return "/var/lib/containerd-test", nil }
	for _, alias := range []string{"--socket-path", "--state-root"} {
		t.Run(alias, func(t *testing.T) {
			_, err := parseSnapshotterFlagsWithDataRoot([]string{alias, "/tmp/legacy"}, resolver)

			require.Error(t, err)
		})
	}
}

func TestBuildSnapshotterOptions_enablesRemapIDs_whenCapabilityPresentAndSupported(t *testing.T) {
	// Given
	config := snapshotterConfig{
		Capabilities: []string{"remap-ids"},
		SupportsIDMappedMounts: func() (bool, error) {
			return true, nil
		},
	}

	// When
	opts, err := buildSnapshotterOptions(config)

	// Then
	require.NoError(t, err)
	snapshotter, err := overlay.NewSnapshotter(t.TempDir(), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshotter.Close()) })
	require.True(t, overlay.HasRemapIDs(snapshotter))
	require.True(t, overlay.HasRootfsHooks(snapshotter))
}

func TestBuildSnapshotterOptions_enablesRootfsHooks(t *testing.T) {
	// Given
	config := snapshotterConfig{
		Capabilities: []string{},
		SupportsIDMappedMounts: func() (bool, error) {
			return true, nil
		},
	}

	// When
	opts, err := buildSnapshotterOptions(config)

	// Then
	require.NoError(t, err)
	snapshotter, err := overlay.NewSnapshotter(t.TempDir(), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshotter.Close()) })
	require.True(t, overlay.HasRootfsHooks(snapshotter))
}

func TestBuildSnapshotterOptions_returnsError_whenCapabilityPresentAndUnsupported(t *testing.T) {
	// Given
	config := snapshotterConfig{
		Capabilities: []string{"remap-ids"},
		SupportsIDMappedMounts: func() (bool, error) {
			return false, nil
		},
	}

	// When
	_, err := buildSnapshotterOptions(config)

	// Then
	require.ErrorIs(t, err, ErrIDMappedMountsUnsupported)
}
