package yandex_telemost_squares

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/core/framing"
	"github.com/Esteviredzu/shampoore-core/internal/core/reliability/kcp"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/provider/telemost"
)

var defaults = app.Values{
	"mux_flush_ms":        10,
	"mux_flush_threshold": 4000,

	"kcp_conv":     0,
	"kcp_mtu":      390,
	"kcp_window":   512,
	"kcp_nodelay":  1,
	"kcp_interval": 5,
	"kcp_resend":   2,
	"kcp_nc":       true,

	"fec_enabled":       false,
	"fec_data_shards":   10,
	"fec_parity_shards": 3,

	"squares_bits_per_cell": 4,
	"squares_cell_size":     16,
	"squares_cols":          120,
	"squares_rows":          80,
	"squares_fps":           45,
	"squares_bitrate":       "4000K",
	"squares_copies":        1,
}

func init() {
	err := app.RegisterDescriptor(app.Descriptor{
		Kind:        "yandex_telemost_squares",
		DisplayName: "Yandex Telemost (framing + KCP)",
		Defaults:    defaults,
		DecodeURL:   telemost.DecodeURL,
		EncodeURL:   telemost.EncodeURL,
		Build:       build,
	})
	if err != nil {
		panic(err)
	}
}

func build(
	ctx context.Context,
	s *app.Settings,
	role string,
	logger logging.ContextLogger,
	eff app.Values,
) (*app.BuildResult, error) {
	bitsPerCell := eff.Int("squares_bits_per_cell", 4)
	if err := framing.InitPalette(bitsPerCell); err != nil {
		return nil, fmt.Errorf("yandex_telemost_squares: bits_per_cell: %w", err)
	}

	cfg := telemost.DefaultConfig()
	if data := s.TransportData(); len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	boot, err := telemost.Bootstrap(ctx, &cfg)
	if err != nil {
		return nil, err
	}

	pipe := telemost.NewPipe(&cfg, boot, logger)
	if err := pipe.Start(ctx); err != nil {
		return nil, fmt.Errorf("yandex_telemost_squares: signaling start: %w", err)
	}
	tunnel := pipe.Tunnel()
	if tunnel == nil {
		_ = pipe.Stop(ctx)
		return nil, fmt.Errorf("yandex_telemost_squares: tunnel is nil")
	}

	cellSize := eff.Int("squares_cell_size", 16)
	cols := eff.Int("squares_cols", 120)
	rows := eff.Int("squares_rows", 80)
	fps := eff.Int("squares_fps", 45)
	bitrate := eff.String("squares_bitrate", "4000K")
	copies := eff.Int("squares_copies", 1)

	grid := framing.Grid{CellSize: cellSize, Cols: cols, Rows: rows}
	w, h, _, _, maxMsg, err := grid.DeriveLayout()
	if err != nil {
		_ = pipe.Stop(ctx)
		return nil, err
	}

	mtu := eff.Int("kcp_mtu", 390)
	if mtu > maxMsg {
		logger.Info("yandex_telemost_squares: kcp_mtu clamped ", mtu, " -> ", maxMsg)
		mtu = maxMsg
	}
	if mtu < 64 {
		_ = pipe.Stop(ctx)
		return nil, fmt.Errorf("yandex_telemost_squares: kcp_mtu too small: %d", mtu)
	}

	sqCfg := framing.Config{
		Width:    w,
		Height:   h,
		Cols:     cols,
		Rows:     rows,
		CellSize: cellSize,
		FPS:      fps,
		Bitrate:  bitrate,
		Copies:   copies,
	}
	sqPipe, err := framing.NewPipe(tunnel, sqCfg)
	if err != nil {
		_ = pipe.Stop(ctx)
		return nil, err
	}
	if err := sqPipe.Start(ctx); err != nil {
		_ = pipe.Stop(ctx)
		return nil, err
	}

	conv := uint32(eff.Int("kcp_conv", 0))
	if conv == 0 {
		conv = crc32.ChecksumIEEE([]byte(cfg.ConferenceLink))
		if conv == 0 {
			conv = 1
		}
	}

	kcpCfg := kcp.Config{
		Conv:     conv,
		MTU:      mtu,
		Window:   eff.Int("kcp_window", 512),
		NoDelay:  eff.Int("kcp_nodelay", 1),
		Interval: eff.Int("kcp_interval", 5),
		Resend:   eff.Int("kcp_resend", 2),
		NC:       eff.Bool("kcp_nc", true),

		FECEnabled:      eff.Bool("fec_enabled", false),
		FECDataShards:   eff.Int("fec_data_shards", 10),
		FECParityShards: eff.Int("fec_parity_shards", 3),
	}
	inner, err := kcp.NewSession(sqPipe, logger, kcpCfg)
	if err != nil {
		_ = sqPipe.Stop(ctx)
		_ = pipe.Stop(ctx)
		return nil, err
	}
	if err := inner.Start(ctx); err != nil {
		_ = sqPipe.Stop(ctx)
		_ = pipe.Stop(ctx)
		return nil, err
	}

	logger.Info("yandex_telemost_squares: ready (mtu=", mtu, " conv=", conv, ")")

	return &app.BuildResult{
		Transport:    inner,
		Provider:     pipe,
		BypassHosts:  pipe.BypassHosts(),
		ConnectedIPs: pipe.ConnectedIPs,
	}, nil
}