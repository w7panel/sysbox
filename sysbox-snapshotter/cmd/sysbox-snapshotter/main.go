/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/containerd/v2/contrib/snapshotservice"
	"github.com/containerd/log"
	sddaemon "github.com/coreos/go-systemd/v22/daemon"
	sysboxsnapshotter "github.com/w7panel/sysbox/sysbox-snapshotter"
	"github.com/w7panel/sysbox/sysbox-snapshotter/cmd/sysbox-snapshotter/version"
	"github.com/w7panel/sysbox/sysbox-snapshotter/rootfs"
	"google.golang.org/grpc"
)

// main is from https://github.com/containerd/containerd/blob/b9fad5e310fafb453def5f1e7094f4c36a9806d2/PLUGINS.md
func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("sysbox-snapshotter %s %s\n", version.Version, version.Revision)
		return
	}

	log.L.Infof("sysbox-snapshotter Version=%q Revision=%q", version.Version, version.Revision)
	// Provide a unix address to listen to, this will be the `address`
	// in the `proxy_plugin` configuration.
	// The root will be used to store the snapshots.
	address, root, containerdSocket, ok := parseArgs(os.Args)
	if !ok {
		fmt.Printf("invalid args: usage: %s --socket <unix socket> --root <root> --containerd-socket <socket>\n", os.Args[0])
		os.Exit(1)
	}
	if err := serve(address, root, containerdSocket); err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}

func parseArgs(args []string) (address, root, containerdSocket string, ok bool) {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&address, "socket", "", "snapshotter unix socket")
	flags.StringVar(&root, "root", "", "snapshotter root")
	flags.StringVar(&containerdSocket, "containerd-socket", "", "containerd socket")
	if err := flags.Parse(args[1:]); err != nil {
		return "", "", "", false
	}
	return address, root, containerdSocket, address != "" && root != "" && containerdSocket != ""
}

func serve(address, root, containerdSocket string) error {
	// Prepare address directory
	if err := os.MkdirAll(filepath.Dir(address), 0700); err != nil {
		return err
	}
	// Remove the socket if exist to avoid EADDRINUSE
	if err := os.RemoveAll(address); err != nil {
		return err
	}
	// Instantiate the snapshotter
	if err := sysboxsnapshotter.Supported(root); err != nil {
		return err
	}
	sidecarStore := rootfs.NewContainerdSidecarSpecStore(containerdSocket)
	sn, err := sysboxsnapshotter.NewSnapshotter(root, sysboxsnapshotter.WithRootfsHooks(sysboxsnapshotter.RootfsHooks{
		IdentityResolver: rootfs.NewContainerdIdentityResolver(containerdSocket),
		MetadataResolver: rootfs.NewSidecarMetadataResolver(sidecarStore),
		PVCResolver:      rootfs.NewPVCMountPathResolver(sidecarStore),
		Preparer:         rootfs.NewLocalPreparer(),
	}))
	if err != nil {
		return err
	}

	// Convert the snapshotter to a gRPC service,
	// example in github.com/containerd/containerd/contrib/snapshotservice
	rpc := grpc.NewServer()
	service := snapshotservice.FromSnapshotter(sn)

	// Register the service with the gRPC server
	snapshotsapi.RegisterSnapshotsServer(rpc, service)

	// Listen and serve
	l, err := net.Listen("unix", address)
	if err != nil {
		return err
	}
	if os.Getenv("NOTIFY_SOCKET") != "" {
		sddaemon.SdNotify(false, sddaemon.SdNotifyReady)
		defer sddaemon.SdNotify(false, sddaemon.SdNotifyStopping)
	}
	return rpc.Serve(l)
}
