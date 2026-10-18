// FILE: internal/transport/yandex_telemost/register.go
package telemost

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport/squares"
)

func init() {
	defaults := app.Values{
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


// 	defaults := app.Values{
//     "mux_flush_ms":        20,    // было 10 — пусть mux батчит под длительность кадра
//     "mux_flush_threshold": 4600,  // было 4000 — почти maxData, чтобы одна пачка влезала в один VP8-кадр

//     "kcp_conv":     0,
//     "kcp_mtu":      1200,   // было 390 — KCP-сегмент почти максимального размера
//     "kcp_window":   256,
//     "kcp_nodelay":  1,
//     "kcp_interval": 5,
//     "kcp_resend":   5,
//     "kcp_nc":       true,

//     // ── Сетка: те же 960×640, но 4× плотнее ──
//     "squares_cell_size": 8,        // было 16
//     "squares_cols":      120,      // было 60
//     "squares_rows":      80,       // было 40
//     "squares_fps":       30,
//     "squares_bitrate":   "6000K",  // было 4000K — при 4× данных нужно чуть больше
//     "squares_copies":    1,
// }
	err := app.RegisterDescriptor(app.Descriptor{
		Kind:        "telemost",
		DisplayName: "Yandex Telemost (squares + KCP)",
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

	// ─── Сетка: всё выводится из одной тройки (cellSize, cols, rows) ───
	sqCellSize := eff.Int("squares_cell_size", 8)
	sqCols := eff.Int("squares_cols", 120)
	sqRows := eff.Int("squares_rows", 80)
	sqFPS := eff.Int("squares_fps", 30)
	sqBitrate := eff.String("squares_bitrate", "8000K")
	sqCopies := eff.Int("squares_copies", 1)

	grid := squares.Grid{
		CellSize: sqCellSize,
		Cols:     sqCols,
		Rows:     sqRows,
	}
	sqW, sqH, payloadBytes, _, maxMsg, err := grid.DeriveLayout()
	if err != nil {
		return nil, err
	}

	// kcp_mtu не может быть больше, чем влезает в одно сообщение.
	mtu := eff.Int("kcp_mtu", 390)
	if mtu > maxMsg {
		logger.Info("telemost: kcp_mtu clamped ", mtu, " -> ", maxMsg)
		mtu = maxMsg
	}
	if mtu < 64 {
		return nil, fmt.Errorf("telemost: kcp_mtu too small: %d", mtu)
	}

	logger.Info("telemost: squares grid: ", sqCols, "x", sqRows,
		" cell=", sqCellSize, "px",
		" res=", sqW, "x", sqH,
		" payload=", payloadBytes, "B/frame",
		" fps=", sqFPS,
		" bitrate=", sqBitrate,
		" max_msg=", maxMsg,
		" kcp_mtu=", mtu)

	session := NewSession(&cfg, boot, logger, SessionOptions{
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

		UseSharing: false,
	})
	return &app.BuildResult{Transport: session}, nil
}