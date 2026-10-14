package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing/tunnel"
	"github.com/Esteviredzu/shampoore-core/internal/socks5"

	_ "github.com/Esteviredzu/shampoore-core/internal/transport/yandex"
	_ "github.com/Esteviredzu/shampoore-core/internal/transport/yandex_telemost"
)

func main() {
	configPath := flag.String("c", "config.json", "config file")
	encode := flag.Bool("encode", false, "encode config as shampoore:// URL and exit")
	flag.Parse()

	if *encode {
		s, err := app.Load(*configPath)
		if err != nil {
			log.Fatalf("load: %v", err)
		}
		link, err := app.EncodeURL(s)
		if err != nil {
			log.Fatalf("encode: %v", err)
		}
		fmt.Println(link)
		return
	}

	s, err := app.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	debug := app.LogLevel == "debug" || app.LogLevel == "trace"
	lg := logging.New("server", debug)

	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()

	rt, err := app.BuildRuntime(ctx, s, "server", lg)
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

	handler := socks5.NewRemoteHandler(ctx, mux, lg, 8000, app.IPv4Only)
	go func() {
		if err := handler.Run(); err != nil {
			log.Printf("handler stopped: %v", err)
		}
	}()

	println("server: ready")
	<-ctx.Done()

	handler.Stop()
	_ = rt.Stop(context.Background())
	_ = mux.Stop(context.Background())
}