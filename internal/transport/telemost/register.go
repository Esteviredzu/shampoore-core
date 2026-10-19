package telemost

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/core/framing"
	"github.com/Esteviredzu/shampoore-core/internal/core/reliability/kcp"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

var telemostDefaults = app.Values{
	"mux_flush_ms":        10,
	"mux_flush_threshold": 4000,

	"kcp_conv":     0,
	"kcp_mtu":      390,
	"kcp_window":   256,
	"kcp_nodelay":  1,
	"kcp_interval": 5,
	"kcp_resend":   5,
	"kcp_nc":       true,

	"squares_cell_size": 16,
	"squares_cols":      120,
	"squares_rows":      80,
	"squares_fps":       30,
	"squares_bitrate":   "4000K",
	"squares_copies":    1,
}

func init() {
	err := app.RegisterDescriptor(app.Descriptor{
		Kind:        "telemost",
		DisplayName: "Yandex Telemost (framing + KCP)",
		Defaults:    telemostDefaults,
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
	logger.Info("telemost: peer_id=", boot.info.PeerID,
		" room_id=", boot.info.RoomID)
	logger.Info("telemost: media_server=", boot.info.MediaServerURL)

	conv := uint32(eff.Int("kcp_conv", 0))
	if conv == 0 {
		conv = crc32.ChecksumIEEE([]byte(cfg.ConferenceLink))
		if conv == 0 {
			conv = 1
		}
	}

	sqCellSize := eff.Int("squares_cell_size", 16)
	sqCols := eff.Int("squares_cols", 120)
	sqRows := eff.Int("squares_rows", 80)
	sqFPS := eff.Int("squares_fps", 30)
	sqBitrate := eff.String("squares_bitrate", "4000K")
	sqCopies := eff.Int("squares_copies", 1)

	grid := framing.Grid{
		CellSize: sqCellSize,
		Cols:     sqCols,
		Rows:     sqRows,
	}
	sqW, sqH, payloadBytes, _, maxMsg, err := grid.DeriveLayout()
	if err != nil {
		return nil, err
	}

	mtu := eff.Int("kcp_mtu", 390)
	if mtu > maxMsg {
		logger.Info("telemost: kcp_mtu clamped ", mtu, " -> ", maxMsg)
		mtu = maxMsg
	}
	if mtu < 64 {
		return nil, fmt.Errorf("telemost: kcp_mtu too small: %d", mtu)
	}

	logger.Info("telemost: framing grid: ", sqCols, "x", sqRows,
		" cell=", sqCellSize, "px",
		" res=", sqW, "x", sqH,
		" payload=", payloadBytes, "B/frame",
		" fps=", sqFPS,
		" bitrate=", sqBitrate,
		" max_msg=", maxMsg,
		" kcp_mtu=", mtu)

	provider := NewSession(&cfg, boot, logger, SessionOptions{
		KcpConv:     conv,
		KcpMTU:      mtu,
		KcpWindow:   eff.Int("kcp_window", 256),
		KcpNoDelay:  eff.Int("kcp_nodelay", 1),
		KcpInterval: eff.Int("kcp_interval", 5),
		KcpResend:   eff.Int("kcp_resend", 5),
		KcpNC:       eff.Bool("kcp_nc", true),

		SquaresWidth:    sqW,
		SquaresHeight:   sqH,
		SquaresCols:     sqCols,
		SquaresRows:     sqRows,
		SquaresCellSize: sqCellSize,
		SquaresFPS:      sqFPS,
		SquaresBitrate:  sqBitrate,
		SquaresCopies:   sqCopies,

		// Propagate the user's UseSharing preference from transport
		// config. Previously this was hard-coded to false, silently
		// ignoring {"use_sharing": true} in the JSON.
		UseSharing: cfg.UseSharing,
	})
	if err := provider.Start(ctx); err != nil {
		return nil, err
	}
	tunnel := provider.Tunnel()
	if tunnel == nil {
		_ = provider.Stop(ctx)
		return nil, fmt.Errorf("telemost: tunnel is nil")
	}

	sqCfg := framing.Config{
		Width:    sqW,
		Height:   sqH,
		Cols:     sqCols,
		Rows:     sqRows,
		CellSize: sqCellSize,
		FPS:      sqFPS,
		Bitrate:  sqBitrate,
		Copies:   sqCopies,
	}
	sqPipe, err := framing.NewPipe(tunnel, sqCfg)
	if err != nil {
		_ = provider.Stop(ctx)
		return nil, err
	}
	if err := sqPipe.Start(ctx); err != nil {
		_ = provider.Stop(ctx)
		return nil, err
	}
	logger.Info("telemost: framing pipe started; max_msg=", sqPipe.MaxMessageSize())

	kcpCfg := kcp.Config{
		Conv:     conv,
		MTU:      mtu,
		Window:   eff.Int("kcp_window", 256),
		NoDelay:  eff.Int("kcp_nodelay", 1),
		Interval: eff.Int("kcp_interval", 5),
		Resend:   eff.Int("kcp_resend", 5),
		NC:       eff.Bool("kcp_nc", true),
	}
	inner, err := kcp.NewSession(sqPipe, logger, kcpCfg)
	if err != nil {
		_ = sqPipe.Stop(ctx)
		_ = provider.Stop(ctx)
		return nil, err
	}
	if err := inner.Start(ctx); err != nil {
		_ = sqPipe.Stop(ctx)
		_ = provider.Stop(ctx)
		return nil, err
	}

	logger.Info("telemost: ready (framing + kcp, conv=", conv,
		" mtu=", mtu, " window=", eff.Int("kcp_window", 256), ")")

	return &app.BuildResult{
		Transport: inner,
		Provider:  provider,
	}, nil
}