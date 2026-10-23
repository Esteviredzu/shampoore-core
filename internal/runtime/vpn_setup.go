package runtime

import (
	"log"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
	"github.com/Esteviredzu/shampoore-core/internal/runtime/muxproxy"
)

func ComputeBypassHosts(s *app.Settings, rt *app.Runtime) []string {
	bypass := append([]string{}, s.TUN.BypassHosts...)
	bypass = append(bypass, rt.BypassHosts...)
	if rt.ConnectedIPs != nil {
		if extra := rt.ConnectedIPs(); len(extra) > 0 {
			log.Printf("tun: transport reports %d connected peer IPs: %v",
				len(extra), extra)
			bypass = append(bypass, extra...)
		}
	}
	return DedupStrings(bypass)
}


func DedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func AttachMuxForTUN(mux multiplexing.StreamMultiplexer) {
	muxproxy.SetMux(mux, app.IPv4Only)
}