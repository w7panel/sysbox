package rootfs

import "context"

type RootfsPreparer interface {
	PrepareRootfsRwLayer(ctx context.Context, request PrepareRootfsRequest) (PreparedRootfs, error)
}
