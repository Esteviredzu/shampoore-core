package mobile

import (
	"context"
	"sync"
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

var (
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
)

func StartClient(source string) string {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		return "shampoore: client already running"
	}
	if err := startLocked(source, 0); err != nil {
		return "shampoore: " + err.Error()
	}
	return ""
}

func StartClientVPN(source string, fd int) string {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		return "shampoore: client already running"
	}
	if err := startLocked(source, fd); err != nil {
		return "shampoore: " + err.Error()
	}
	return ""
}

func startLocked(source string, vpnFD int) error {
	s, err := app.ParseSource(source)
	if err != nil {
		return err
	}
	lg := logging.New("mobile", app.LogLevel == "debug")

	ctx, cancelFn := context.WithCancel(context.Background())

	rt, err := app.BuildRuntime(ctx, s, "client", lg)
	if err != nil {
		cancelFn()
		return err
	}
	muxCfg := tunnel.Config{
		FlushThreshold: rt.MuxConfig.FlushThreshold,
		FlushInterval:  time.Duration(rt.MuxConfig.FlushMs) * time.Millisecond,
	}
	mux := tunnel.New(rt.Transport, lg, muxCfg)

	if err := mux.Start(ctx); err != nil {
		cancelFn()
		return err
	}
	if err := rt.Start(ctx); err != nil {
		_ = mux.Stop(context.Background())
		cancelFn()
		return err
	}

	srv := socks5.NewLocalServer(s.SOCKS5.Listen, mux, lg, app.IPv4Only)
	go func() { _ = srv.ListenAndServe(ctx) }()

	if vpnFD > 0 {
		runtime.AttachMuxForTUN(mux)
		go func() {
			if err := runtime.StartTUNFD(ctx, vpnFD, s.TUN.MTU); err != nil {
				lg.Warn("mobile: TUN stopped: ", err)
			}
		}()
	}

	cancel = cancelFn
	done = make(chan struct{})
	go func() {
		<-ctx.Done()
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelStop()
		_ = rt.Stop(stopCtx)
		_ = mux.Stop(stopCtx)
		close(done)
	}()
	return nil
}

func StopClient() {
	mu.Lock()
	cancelFn := cancel
	d := done
	cancel = nil
	done = nil
	mu.Unlock()
	if cancelFn == nil {
		return
	}
	cancelFn()
	if d != nil {
		select {
		case <-d:
		case <-time.After(7 * time.Second):
		}
	}
}

func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return cancel != nil
}