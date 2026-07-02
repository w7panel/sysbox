package rootfs

import (
	"context"
	"errors"
)

var ErrRootfsRwLayerNotConfigured = errors.New("rootfs rw-layer not configured")

type MetadataResolver interface {
	ResolveRootfsRwLayer(ctx context.Context, request RootfsRwLayerRequest) (RootfsRwLayerSpec, error)
}
