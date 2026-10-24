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
	eff := s.EffectiveAdvanced()
	tunCfg := TUNConfig{
		Name:        eff.String("tun_name", "shampoore0"),
		MTU:         eff.Int("tun_mtu", 1500),
		Inet4:       eff.String("tun_inet4", "172.19.0.1/30"),
		AutoRoute:   eff.Bool("tun_auto_route", true),
		BypassHosts: ComputeBypassHosts(s, rt),
		DNSServers:  eff.StringSlice("tun_dns", []string{"1.1.1.1:53", "8.8.8.8:53"}),
	}
	AttachMuxForTUN(mux)
	return StartTUN(ctx, tunCfg, mux)
}