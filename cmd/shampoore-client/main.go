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
	"github.com/Esteviredzu/shampoore-core/internal/socks5"

	_ "github.com/Esteviredzu/shampoore-core/internal/transport/yandex"
	_ "github.com/Esteviredzu/shampoore-core/internal/transport/yandex_telemost_raw"
	_ "github.com/Esteviredzu/shampoore-core/internal/transport/yandex_telemost_squares"
)

func main() {
	configPath := flag.String("c", "config.json", "config file or shampoore:// URL")
	modeFlag := flag.String("m", "socks5", "mode: socks5|tun|both")
	flag.Parse()

	mode := app.Mode(*modeFlag)
	switch mode {
	case app.ModeSOCKS5, app.ModeTUN, app.ModeBoth:
	default:
		log.Fatalf("mode: invalid %q (want socks5|tun|both)", *modeFlag)
	}

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

	eff := s.EffectiveAdvanced()

	if mode == app.ModeSOCKS5 || mode == app.ModeBoth {
		listen := eff.String("socks5_listen", "127.0.0.1:1080")
		srv := socks5.NewLocalServer(listen, mux, lg, app.IPv4Only)
		go func() {
			if err := srv.ListenAndServe(ctx); err != nil {
				log.Printf("socks5 stopped: %v", err)
			}
		}()
		println("socks5 listening on", listen)
	}

	if mode == app.ModeTUN || mode == app.ModeBoth {
		go func() {
			if err := runtime.StartClientTUN(ctx, s, rt, mux); err != nil {
				log.Printf("tun stopped: %v", err)
			}
		}()
		println("tun enabled on", eff.String("tun_name", "shampoore0"))
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