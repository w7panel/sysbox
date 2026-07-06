package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/contrib/snapshotservice"
	"github.com/containerd/containerd/v2/core/snapshots"
	"google.golang.org/grpc"
)

type Config struct {
	Address     string
	Snapshotter snapshots.Snapshotter
}

type Server struct {
	config Config
}

func NewServer(config Config) *Server {
	return &Server{config: config}
}

func (s *Server) Serve(ctx context.Context) error {
	if s.config.Address == "" {
		return fmt.Errorf("snapshotter socket address is required")
	}
	if s.config.Snapshotter == nil {
		return fmt.Errorf("snapshotter is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.config.Address), 0o700); err != nil {
		return fmt.Errorf("create snapshotter socket directory: %w", err)
	}
	if err := removeStaleSocket(s.config.Address); err != nil {
		return fmt.Errorf("remove stale snapshotter socket: %w", err)
	}
	listener, err := net.Listen("unix", s.config.Address)
	if err != nil {
		return fmt.Errorf("listen on snapshotter socket: %w", err)
	}
	defer listener.Close()

	grpcServer := grpc.NewServer()
	snapshotsapi.RegisterSnapshotsServer(grpcServer, snapshotservice.FromSnapshotter(s.config.Snapshotter))
	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve snapshotter grpc: %w", err)
	}
	return nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a unix socket", path)
	}
	return os.Remove(path)
}
