package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"syscall"

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
	defaultSysboxSnapshotterRoot = "/var/lib/rancher/k3s/agent/containerd/io.containerd.snapshotter.v1.sysbox"
	defaultContainerdConfig      = "/var/lib/rancher/k3s/agent/etc/containerd/config.toml"
	defaultContainerdSocket      = "/run/k3s/containerd/containerd.sock"
	defaultKubeletPodsPath       = "/var/lib/kubelet/pods"
	proxyPluginID                = "sysbox"
)

var ErrIDMappedMountsUnsupported = errors.New("idmapped overlay mounts are not supported")

type snapshotterConfig struct {
	Capabilities           []string
	KubeletPodsPath        string
	ContainerdSocket       string
	SupportsIDMappedMounts func() (bool, error)
}

type snapshotterFlags struct {
	Address          string
	Root             string
	ContainerdConfig string
	KubeletPodsPath  string
	ShowVersion      bool
}

func main() {
	if err := run(); err != nil {
		slog.Error("sysbox-snapshotter failed", slog.Any("err", err))
		os.Exit(1)
	}
}

func run() error {
	flags, err := parseSnapshotterFlags(os.Args[1:])
	if err != nil {
		return err
	}

	if flags.ShowVersion {
		fmt.Printf("sysbox-snapshotter %s %s %s %s %s\n", edition, version, commitId, builtAt, builtBy)
		return nil
	}

	capabilities, err := readProxyCapabilities(flags.ContainerdConfig, proxyPluginID)
	if err != nil {
		return err
	}

	opts, err := buildSnapshotterOptions(snapshotterConfig{
		Capabilities:           capabilities,
		KubeletPodsPath:        flags.KubeletPodsPath,
		ContainerdSocket:       defaultContainerdSocket,
		SupportsIDMappedMounts: overlayutils.SupportsIDMappedMounts,
	})
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

func parseSnapshotterFlags(args []string) (snapshotterFlags, error) {
	flags := snapshotterFlags{}
	flagSet := flag.NewFlagSet("sysbox-snapshotter", flag.ContinueOnError)
	flagSet.StringVar(&flags.Address, "address", "/run/sysbox-snapshotter.sock", "unix socket address for containerd proxy plugin")
	flagSet.StringVar(&flags.Root, "root", defaultSysboxSnapshotterRoot, "sysbox snapshotter root directory")
	flagSet.StringVar(&flags.ContainerdConfig, "containerd-config", defaultContainerdConfig, "containerd config path used to read sysbox proxy capabilities")
	flagSet.StringVar(&flags.KubeletPodsPath, "kubelet-pods-path", defaultKubeletPodsPath, "kubelet pods directory used to resolve PVC mount paths")
	flagSet.BoolVar(&flags.ShowVersion, "version", false, "print version and exit")
	return flags, flagSet.Parse(args)
}

func buildSnapshotterOptions(config snapshotterConfig) ([]overlay.Opt, error) {
	opts := []overlay.Opt{overlay.AsynchronousRemove}
	sidecarStore := rootfs.NewContainerdSidecarSpecStore(config.ContainerdSocket)
	opts = append(opts, overlay.WithRootfsHooks(overlay.RootfsHooks{
		IdentityResolver: rootfs.NewContainerdIdentityResolver(),
		MetadataResolver: rootfs.NewSidecarMetadataResolver(sidecarStore),
		PVCResolver: rootfs.NewComposedPVCMountPathResolver(
			rootfs.NewPVCMountPathResolver(sidecarStore),
			rootfs.NewKubeletPVCMountPathResolver(config.KubeletPodsPath),
		),
		Preparer: rootfs.NewLocalPreparer(),
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

func readProxyCapabilities(configPath string, pluginID string) ([]string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read containerd config: %w", err)
	}
	section := sectionBody(string(data), fmt.Sprintf("[proxy_plugins.%s]", pluginID))
	if section == "" {
		section = sectionBody(string(data), fmt.Sprintf("[proxy_plugins.%q]", pluginID))
	}
	return parseCapabilities(section), nil
}

func parseCapabilities(section string) []string {
	match := regexp.MustCompile(`(?m)^\s*capabilities\s*=\s*\[(.*)\]\s*$`).FindStringSubmatch(section)
	if len(match) != 2 {
		return nil
	}
	items := strings.Split(match[1], ",")
	capabilities := make([]string, 0, len(items))
	for _, item := range items {
		capability := strings.Trim(strings.TrimSpace(item), `"`)
		if capability != "" {
			capabilities = append(capabilities, capability)
		}
	}
	return capabilities
}

func sectionBody(config string, header string) string {
	_, rest, found := strings.Cut(config, header)
	if !found {
		return ""
	}
	body, _, found := strings.Cut(rest, "\n[")
	if !found {
		return rest
	}
	return body
}

func hasCapability(capabilities []string, capability string) bool {
	return slices.Contains(capabilities, capability)
}
