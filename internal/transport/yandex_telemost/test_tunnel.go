// FILE: internal/transport/yandex_telemost/test_tunnel.go
package telemost

import (
	"context"
	"errors"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport/vp8"
)

// OpenTestTunnel открывает vp8.Tunnel в обход KCP-стека. Используется
// тестовыми утилитами (squares/upload, squares/download), которые работают
// с сырыми VP8-кадрами напрямую.
func OpenTestTunnel(
	ctx context.Context,
	conferenceLink string,
	logger logging.ContextLogger,
) (*vp8.Tunnel, *Pipe, error) {
	cfg := DefaultConfig()
	cfg.ConferenceLink = conferenceLink
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}

	boot, err := bootstrap(ctx, &cfg)
	if err != nil {
		return nil, nil, err
	}
	logger.Info("test-tunnel: peer_id=", boot.info.PeerID,
		" room_id=", boot.info.RoomID)
	logger.Info("test-tunnel: media_server=", boot.info.MediaServerURL)

	pipe := NewPipe(&cfg, boot, logger)
	if err := pipe.Start(ctx); err != nil {
		return nil, nil, err
	}
	tunnel := pipe.Tunnel()
	if tunnel == nil {
		_ = pipe.Stop(ctx)
		return nil, nil, errors.New("test-tunnel: vp8 tunnel not created")
	}
	return tunnel, pipe, nil
}