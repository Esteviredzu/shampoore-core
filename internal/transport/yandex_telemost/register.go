package telemost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport/vp8"
)

func init() {
	app.RegisterTransport("telemost", buildRuntime)
}

var telemostDefaults = app.TransportDefaults{
	MuxFlushMs:        10,
	MuxFlushThreshold: 3000,

	ReliableWindow: 4,
	ReliableChunk:  100,

	PaddingEnabled: true,
	PaddingTarget:  1000,

	JitterMinMs: 0,
	JitterMaxMs: 0,

	VP8SendDelayMs: 100,
}

func buildRuntime(
	ctx context.Context,
	s *app.Settings,
	role string,
	logger logging.ContextLogger,
) (*app.Runtime, error) {
	cfg := DefaultConfig()
	if data := s.TransportData(); len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	eff := s.Advanced.Effective(telemostDefaults)

	boot, err := bootstrap(ctx, &cfg)
	if err != nil {
		return nil, err
	}
	logger.Info("telemost: peer_id=", boot.info.PeerID, " room_id=", boot.info.RoomID)
	logger.Info("telemost: media_server=", boot.info.MediaServerURL)

	vp8Opts := vp8.PipeOptions{SendDelayMs: eff.VP8SendDelayMs}
	logger.Info("telemost: vp8 send delay = ", eff.VP8SendDelayMs, "ms")

	telePipe := NewPipe(&cfg, boot, logger, vp8Opts)

	if err := telePipe.Start(ctx); err != nil {
		return nil, err
	}

	tunnel := telePipe.Tunnel()
	if tunnel == nil {
		return nil, errors.New("telemost: vp8 tunnel not created")
	}
	logger.Info("telemost: vp8 tunnel attached")

	kcp := vp8.NewKCPPipe(tunnel, eff.VP8SendDelayMs)
	if err := kcp.Start(ctx); err != nil {
		return nil, err
	}

	rt := app.NewRuntime(kcp, nil, nil)
	rt.MuxConfig = app.MuxConfig{
		FlushMs:        eff.MuxFlushMs,
		FlushThreshold: eff.MuxFlushThreshold,
	}
	return rt, nil
}