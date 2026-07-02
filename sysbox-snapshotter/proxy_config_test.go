package sysbox_snapshotter_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainerdProxyConfig_omitsRemapCapabilitiesByDefault(t *testing.T) {
	// Given
	config := readContainerdConfig(t)

	// When
	sysboxSection := sectionBody(config, `[proxy_plugins."sysbox"]`)

	// Then
	require.Contains(t, sysboxSection, `type = "snapshot"`)
	require.NotContains(t, sysboxSection, `remap-ids`)
	require.NotContains(t, sysboxSection, `only-remap-ids`)
}

func TestContainerdProxyConfig_keepsGlobalImageSnapshotterUnchanged(t *testing.T) {
	// Given
	config := readContainerdConfig(t)

	// When
	imageSection := sectionBody(config, `[plugins.'io.containerd.cri.v1.images']`)

	// Then
	require.NotContains(t, imageSection, `snapshotter = "sysbox"`)
}

func readContainerdConfig(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile("containerd-sysbox-snapshotter.toml")
	require.NoError(t, err)
	return string(data)
}

func sectionBody(config string, header string) string {
	start := strings.Index(config, header)
	if start == -1 {
		return ""
	}
	rest := config[start+len(header):]
	next := strings.Index(rest, "\n[")
	if next == -1 {
		return rest
	}
	return rest[:next]
}
