package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoveStaleSocket_removesUnixSocket(t *testing.T) {
	// Given: a stale unix socket exists at the target path.
	socketPath := filepath.Join(t.TempDir(), "sysbox-snapshotter.sock")
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	require.NoError(t, listener.Close())

	// When: the snapshotter removes the stale socket.
	err = removeStaleSocket(socketPath)

	// Then: the stale socket is removed successfully.
	require.NoError(t, err)
	require.NoFileExists(t, socketPath)
}

func TestRemoveStaleSocket_rejectsRegularFile_withoutDeletingIt(t *testing.T) {
	// Given: the target path points at a regular file.
	socketPath := filepath.Join(t.TempDir(), "sysbox-snapshotter.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("not a socket"), 0o600))

	// When: the snapshotter checks the stale socket path.
	err := removeStaleSocket(socketPath)

	// Then: the file is rejected and preserved.
	require.Error(t, err)
	data, readErr := os.ReadFile(socketPath)
	require.NoError(t, readErr)
	require.Equal(t, []byte("not a socket"), data)
}

func TestRemoveStaleSocket_rejectsDirectory_withoutDeletingIt(t *testing.T) {
	// Given: the target path points at a directory.
	socketPath := filepath.Join(t.TempDir(), "sysbox-snapshotter.sock")
	require.NoError(t, os.Mkdir(socketPath, 0o700))

	// When: the snapshotter checks the stale socket path.
	err := removeStaleSocket(socketPath)

	// Then: the directory is rejected and preserved.
	require.Error(t, err)
	info, statErr := os.Stat(socketPath)
	require.NoError(t, statErr)
	require.True(t, info.IsDir())
}
