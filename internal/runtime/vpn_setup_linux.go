//go:build linux && !android

package runtime

import (
	"context"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

func StartClientTUN(
	ctx context.Context,
	s *app.Settings,
	rt *app.Runtime,
	mux multiplexing.StreamMultiplexer,
) error {
	tunCfg := TUNConfig{
		Name:        s.TUN.Name,
		MTU:         s.TUN.MTU,
		Inet4:       s.TUN.Inet4,
		AutoRoute:   s.TUN.AutoRoute,
		BypassHosts: ComputeBypassHosts(s, rt),
		DNSServers:  s.TUN.DNSServers,
	}
	AttachMuxForTUN(mux)
	return StartTUN(ctx, tunCfg, mux)
}