// FILE: internal/transport/yandex_telemost/register.go
package telemost

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

func init() {
	defaults := app.Values{
		"mux_flush_ms":        10,
		"mux_flush_threshold": 4000,

		"kcp_conv":     0,
		"kcp_mtu":      390,
		"kcp_window":   256, // was 128 — best in speedtest (w256_i5_r5 = 8.8 KiB/s vs baseline 4.7)
		"kcp_nodelay":  1,
		"kcp_interval": 5,
		"kcp_resend":   5,
		"kcp_nc":       true,

		"squares_w":       640,
		"squares_h":       360,
		"squares_cols":    40,
		"squares_rows":    20,
		"squares_fps":     30,
		"squares_bitrate": "800K",
		"squares_copies":  1,
	}
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
	logger.Info("telemost: peer_id=", boot.info.PeerID, " room_id=", boot.info.RoomID)
	logger.Info("telemost: media_server=", boot.info.MediaServerURL)
	conv := uint32(eff.Int("kcp_conv", 0))
	if conv == 0 {
		conv = crc32.ChecksumIEEE([]byte(cfg.ConferenceLink))
		if conv == 0 {
			conv = 1
		}
	}
	sqW := eff.Int("squares_w", 640)
	sqH := eff.Int("squares_h", 360)
	sqCols := eff.Int("squares_cols", 40)
	sqRows := eff.Int("squares_rows", 20)
	sqFPS := eff.Int("squares_fps", 25)
	sqBitrate := eff.String("squares_bitrate", "600K")
	sqCopies := eff.Int("squares_copies", 1)
	cells := sqCols * sqRows
	payloadBytes := cells / 2
	maxSeg := payloadBytes - 10
	mtu := eff.Int("kcp_mtu", 390)
	if mtu > maxSeg {
		mtu = maxSeg
	}
	if mtu < 64 {
		return nil, fmt.Errorf("telemost: computed kcp_mtu too small: %d", mtu)
	}
	logger.Info("telemost: config squares=", sqCols, "x", sqRows,
		" res=", sqW, "x", sqH,
		" fps=", sqFPS,
		" cells=", cells,
		" max_msg=", maxSeg,
		" copies=", sqCopies,
		" kcp_mtu=", mtu,
		" kcp_resend=", eff.Int("kcp_resend", 5),
		" kcp_interval=", eff.Int("kcp_interval", 5),
		" kcp_nc=", eff.Bool("kcp_nc", true))
	useSharing := false
	logger.Info("telemost: role=", role, " useSharing=", useSharing)
	session := NewSession(&cfg, boot, logger, SessionOptions{
		KcpConv:     conv,
		KcpMTU:      mtu,
		KcpWindow:   eff.Int("kcp_window", 256),
		KcpNoDelay:  eff.Int("kcp_nodelay", 1),
		KcpInterval: eff.Int("kcp_interval", 5),
		KcpResend:   eff.Int("kcp_resend", 5),
		KcpNC:       eff.Bool("kcp_nc", true),
		SquaresWidth:   sqW,
		SquaresHeight:  sqH,
		SquaresCols:    sqCols,
		SquaresRows:    sqRows,
		SquaresFPS:     sqFPS,
		SquaresBitrate: sqBitrate,
		SquaresCopies:  sqCopies,
		UseSharing: useSharing,
	})
	return &app.BuildResult{Transport: session}, nil
}