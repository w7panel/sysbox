package main

import (
	"os"
	"path/filepath"
	"testing"

	overlay "github.com/nestybox/sysbox-snapshotter/snapshotter"
	"github.com/stretchr/testify/require"
)

func TestDefaultSysboxRoot_pointsToDedicatedStateRoot(t *testing.T) {
	const expected = "/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox"
	if defaultSysboxSnapshotterRoot != expected {
		t.Fatalf("default sysbox root = %q, want %q", defaultSysboxSnapshotterRoot, expected)
	}
}

func TestDefaultKubeletPodsPath_pointsToKubeletPodsDirectory(t *testing.T) {
	const expected = "/var/lib/kubelet/pods"
	if defaultKubeletPodsPath != expected {
		t.Fatalf("default kubelet pods path = %q, want %q", defaultKubeletPodsPath, expected)
	}
}

func TestBuildSnapshotterOptions_disablesRemapIDs_whenCapabilityMissing(t *testing.T) {
	// Given
	config := snapshotterConfig{
		Capabilities:    []string{},
		KubeletPodsPath: t.TempDir(),
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
	for _, alias := range []string{"--socket-path", "--state-root"} {
		t.Run(alias, func(t *testing.T) {
			_, err := parseSnapshotterFlags([]string{alias, "/tmp/legacy"})

			require.Error(t, err)
		})
	}
}

func TestBuildSnapshotterOptions_enablesRemapIDs_whenCapabilityPresentAndSupported(t *testing.T) {
	// Given
	config := snapshotterConfig{
		Capabilities:    []string{"remap-ids"},
		KubeletPodsPath: t.TempDir(),
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
		Capabilities:    []string{},
		KubeletPodsPath: t.TempDir(),
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

func TestReadProxyCapabilities_returnsRemapIDs_whenConfigured(t *testing.T) {
	// Given
	configPath := writeContainerdConfig(t, `
[proxy_plugins.sysbox]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
  capabilities = ["remap-ids"]
`)

	// When
	capabilities, err := readProxyCapabilities(configPath, "sysbox")

	// Then
	require.NoError(t, err)
	require.Equal(t, []string{"remap-ids"}, capabilities)
}

func TestReadProxyCapabilities_returnsEmpty_whenCapabilitiesOmitted(t *testing.T) {
	// Given
	configPath := writeContainerdConfig(t, `
[proxy_plugins.sysbox]
  type = "snapshot"
  address = "/run/sysbox-snapshotter.sock"
`)

	// When
	capabilities, err := readProxyCapabilities(configPath, "sysbox")

	// Then
	require.NoError(t, err)
	require.Empty(t, capabilities)
}

func writeContainerdConfig(t *testing.T, config string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
	return path
}
