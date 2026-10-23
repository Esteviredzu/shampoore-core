//go:build !linux && !android

package runtime

import (
	"context"
	"errors"

	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

var errTUNUnsupported = errors.New("tun: not supported on this platform")

func StartTUN(ctx context.Context, cfg TUNConfig, m multiplexing.StreamMultiplexer) error {
	return errTUNUnsupported
}

func StartTUNFD(ctx context.Context, fd int, mtu int) error {
	return errTUNUnsupported
}