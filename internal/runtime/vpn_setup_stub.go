//go:build !linux || android

package runtime

import (
	"context"
	"errors"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

func StartClientTUN(
	ctx context.Context,
	s *app.Settings,
	rt *app.Runtime,
	mux multiplexing.StreamMultiplexer,
) error {
	return errors.New("tun: StartClientTUN is not supported on this platform")
}