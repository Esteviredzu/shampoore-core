package telemost

import (
	"context"
	"encoding/json"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

func init() {
	defaults := app.Values{
		"mux_flush_ms":        10,
		"mux_flush_threshold": 2000,

		"reliable_window": 2,
		"reliable_chunk":  1000,

		"padding_enabled": false,
		"padding_target":  0,

		"jitter_min_ms": 0,
		"jitter_max_ms": 0,

		"vp8_send_delay_ms": 50,
	}

	err := app.RegisterDescriptor(app.Descriptor{
		Kind:        "telemost",
		DisplayName: "Yandex Telemost",
		Defaults:    defaults,
		DecodeURL:   decodeURL,
		EncodeURL:   encodeURL,
		Build:       buildTransport,
	})
	if err != nil {
		panic(err)
	}
}

func buildTransport(
	ctx context.Context,
	s *app.Settings,
	role string,
	logger logging.ContextLogger,
	eff app.Values,
) (*app.BuildResult, error) {
	cfg := DefaultConfig()
	if data := s.TransportData(); len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	boot, err := bootstrap(ctx, &cfg)
	if err != nil {
		return nil, err
	}
	logger.Info("telemost: peer_id=", boot.info.PeerID, " room_id=", boot.info.RoomID)
	logger.Info("telemost: media_server=", boot.info.MediaServerURL)

	session := NewSession(&cfg, boot, logger, SessionOptions{
		VP8SendDelayMs: eff.Int("vp8_send_delay_ms", 50),
		ReliableWindow: eff.Int("reliable_window", 2),
		ReliableChunk:  eff.Int("reliable_chunk", 1000),
	})

	return &app.BuildResult{
		Transport: session,
	}, nil
}