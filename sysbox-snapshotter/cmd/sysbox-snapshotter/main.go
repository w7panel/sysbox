package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/nestybox/sysbox-libs/containerdUtils"
	"github.com/nestybox/sysbox-snapshotter/daemon"
	"github.com/nestybox/sysbox-snapshotter/rootfs"
	overlay "github.com/nestybox/sysbox-snapshotter/snapshotter"
	"github.com/nestybox/sysbox-snapshotter/snapshotter/overlayutils"
)

var (
	edition  = "Community Edition (CE)"
	version  = "unknown"
	commitId = "unknown"
	builtAt  = "unknown"
	builtBy  = "unknown"
)

const (
	sysboxSnapshotterStateDir = "io.containerd.snapshotter.v1.sysbox"
	proxyPluginID             = "sysbox"
)

var ErrIDMappedMountsUnsupported = errors.New("idmapped overlay mounts are not supported")

type snapshotterConfig struct {
	Capabilities           []string
	ContainerdSocket       string
	SupportsIDMappedMounts func() (bool, error)
}

type snapshotterFlags struct {
	Address     string
	Root        string
	ShowVersion bool
}

type runtimeResolvers struct {
	Capabilities func(string) ([]string, error)
	DataRoot     func() (string, error)
	GRPCAddress  func() (string, error)
}

func main() {
	if err := run(); err != nil {
		slog.Error("sysbox-snapshotter failed", slog.Any("err", err))
		os.Exit(1)
	}
}

func run() error {
	flags, config, err := loadRuntimeConfig(os.Args[1:], runtimeResolvers{
		Capabilities: containerdUtils.GetProxyPluginCapabilities,
		DataRoot:     containerdUtils.GetDataRoot,
		GRPCAddress:  containerdUtils.GetGRPCAddress,
	})
	if err != nil {
		return err
	}

	if flags.ShowVersion {
		fmt.Printf("sysbox-snapshotter %s %s %s %s %s\n", edition, version, commitId, builtAt, builtBy)
		return nil
	}

	config.SupportsIDMappedMounts = overlayutils.SupportsIDMappedMounts
	opts, err := buildSnapshotterOptions(config)
	if err != nil {
		return err
	}

	wrapped, err := overlay.NewSnapshotter(flags.Root, opts...)
	if err != nil {
		return err
	}
	defer wrapped.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("starting sysbox snapshotter", slog.String("address", flags.Address), slog.String("root", flags.Root))
	return daemon.NewServer(daemon.Config{Address: flags.Address, Snapshotter: wrapped}).Serve(ctx)
}

func parseSnapshotterFlagsWithDataRoot(args []string, dataRootResolver func() (string, error)) (snapshotterFlags, error) {
	dataRoot, err := dataRootResolver()
	if err != nil {
		return snapshotterFlags{}, fmt.Errorf("read containerd data root: %w", err)
	}
	flags := snapshotterFlags{}
	flagSet := flag.NewFlagSet("sysbox-snapshotter", flag.ContinueOnError)
	flagSet.StringVar(&flags.Address, "address", "/run/sysbox-snapshotter.sock", "unix socket address for containerd proxy plugin")
	flagSet.StringVar(&flags.Root, "root", filepath.Join(dataRoot, sysboxSnapshotterStateDir), "sysbox snapshotter root directory")
	flagSet.BoolVar(&flags.ShowVersion, "version", false, "print version and exit")
	return flags, flagSet.Parse(args)
}

func loadRuntimeConfig(args []string, resolvers runtimeResolvers) (snapshotterFlags, snapshotterConfig, error) {
	flags, err := parseSnapshotterFlagsWithDataRoot(args, resolvers.DataRoot)
	if err != nil {
		return snapshotterFlags{}, snapshotterConfig{}, err
	}
	capabilities, err := resolvers.Capabilities(proxyPluginID)
	if err != nil {
		return snapshotterFlags{}, snapshotterConfig{}, fmt.Errorf("read containerd proxy plugin capabilities: %w", err)
	}
	grpcAddress, err := resolvers.GRPCAddress()
	if err != nil {
		return snapshotterFlags{}, snapshotterConfig{}, fmt.Errorf("read containerd grpc address: %w", err)
	}
	return flags, snapshotterConfig{
		Capabilities:     capabilities,
		ContainerdSocket: grpcAddress,
	}, nil
}

func buildSnapshotterOptions(config snapshotterConfig) ([]overlay.Opt, error) {
	opts := []overlay.Opt{overlay.AsynchronousRemove}
	sidecarStore := rootfs.NewContainerdSidecarSpecStore(config.ContainerdSocket)
	opts = append(opts, overlay.WithRootfsHooks(overlay.RootfsHooks{
		IdentityResolver: rootfs.NewContainerdIdentityResolver(config.ContainerdSocket),
		MetadataResolver: rootfs.NewSidecarMetadataResolver(sidecarStore),
		PVCResolver:      rootfs.NewPVCMountPathResolver(sidecarStore),
		Preparer:         rootfs.NewLocalPreparer(),
	}))
	if hasCapability(config.Capabilities, "remap-ids") {
		supported, err := config.SupportsIDMappedMounts()
		if err != nil {
			return nil, fmt.Errorf("check idmapped overlay mount support: %w", err)
		}
		if !supported {
			return nil, ErrIDMappedMountsUnsupported
		}
		return append(opts, overlay.WithRemapIDs), nil
	}
	return opts, nil
}

func hasCapability(capabilities []string, capability string) bool {
	return slices.Contains(capabilities, capability)
}
