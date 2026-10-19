package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing/tunnel"
	"github.com/Esteviredzu/shampoore-core/internal/runtime"
	"github.com/Esteviredzu/shampoore-core/internal/runtime/muxproxy"
	"github.com/Esteviredzu/shampoore-core/internal/socks5"

	_ "github.com/Esteviredzu/shampoore-core/internal/transport/yandex"
	_ "github.com/Esteviredzu/shampoore-core/internal/transport/telemost"
)

func main() {
	configPath := flag.String("c", "config.json", "config file or shampoore:// URL")
	flag.Parse()

	s, err := app.ParseSource(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	debug := app.LogLevel == "debug" || app.LogLevel == "trace"
	lg := logging.New("client", debug)

	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()

	rt, err := app.BuildRuntime(ctx, s, "client", lg)
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}

	muxCfg := tunnel.Config{
		FlushThreshold: rt.MuxConfig.FlushThreshold,
		FlushInterval:  time.Duration(rt.MuxConfig.FlushMs) * time.Millisecond,
	}
	mux := tunnel.New(rt.Transport, lg, muxCfg)

	if err := mux.Start(ctx); err != nil {
		log.Fatalf("mux start: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		log.Fatalf("runtime start: %v", err)
	}

	if s.SOCKS5Enabled() {
		srv := socks5.NewLocalServer(s.SOCKS5.Listen, mux, lg, app.IPv4Only)
		go func() {
			if err := srv.ListenAndServe(ctx); err != nil {
				log.Printf("socks5 stopped: %v", err)
			}
		}()
		println("socks5 listening on", s.SOCKS5.Listen)
	}

	if s.TUNEnabled() {
		bypass := append([]string{}, s.TUN.BypassHosts...)
		bypass = append(bypass, rt.BypassHosts...)

		tunCfg := runtime.TUNConfig{
			Name:        s.TUN.Name,
			MTU:         s.TUN.MTU,
			Inet4:       s.TUN.Inet4,
			AutoRoute:   s.TUN.AutoRoute,
			BypassHosts: dedup(bypass),
			DNSServers:  s.TUN.DNSServers,
		}

		muxproxy.SetMux(mux, app.IPv4Only)

		go func() {
			if err := runtime.StartTUN(ctx, tunCfg, mux); err != nil {
				log.Printf("tun stopped: %v", err)
			}
		}()
		println("tun enabled on", s.TUN.Name)
	}

	println("client: ready")
	<-ctx.Done()
	println("client: stopping")

	stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStop()

	_ = rt.Stop(stopCtx)
	_ = mux.Stop(stopCtx)

	println("client: stopped")
}

func dedup(in []string) []string {
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