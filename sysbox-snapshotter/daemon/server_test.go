package daemon_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/nestybox/sysbox-snapshotter/daemon"
	"github.com/stretchr/testify/require"
)

type fakeSnapshotter struct{}

func (fakeSnapshotter) Stat(context.Context, string) (snapshots.Info, error) {
	return snapshots.Info{}, nil
}

func (fakeSnapshotter) Update(context.Context, snapshots.Info, ...string) (snapshots.Info, error) {
	return snapshots.Info{}, nil
}

func (fakeSnapshotter) Usage(context.Context, string) (snapshots.Usage, error) {
	return snapshots.Usage{}, nil
}

func (fakeSnapshotter) Mounts(context.Context, string) ([]mount.Mount, error) {
	return nil, nil
}

func (fakeSnapshotter) Prepare(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}

func (fakeSnapshotter) View(context.Context, string, string, ...snapshots.Opt) ([]mount.Mount, error) {
	return nil, nil
}

func (fakeSnapshotter) Commit(context.Context, string, string, ...snapshots.Opt) error {
	return nil
}

func (fakeSnapshotter) Remove(context.Context, string) error {
	return nil
}

func (fakeSnapshotter) Walk(context.Context, snapshots.WalkFunc, ...string) error {
	return nil
}

func (fakeSnapshotter) Close() error {
	return nil
}

func TestServerServe_createsUnixSocket_whenStarted(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "sysbox-snapshotter.sock")
	server := daemon.NewServer(daemon.Config{
		Address:     socketPath,
		Snapshotter: fakeSnapshotter{},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- server.Serve(ctx)
	}()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
}

func TestServerServe_removesStaleSocket_beforeListening(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "sysbox-snapshotter.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("stale"), 0o600))
	server := daemon.NewServer(daemon.Config{
		Address:     socketPath,
		Snapshotter: fakeSnapshotter{},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- server.Serve(ctx)
	}()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, time.Second, 10*time.Millisecond)
	cancel()

	require.NoError(t, <-done)
}
