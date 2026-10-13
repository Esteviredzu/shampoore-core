package yandex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
	"github.com/Esteviredzu/shampoore-core/internal/transport/security"
)

func init() {
	app.RegisterTransport("yandex", buildRuntime)
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

	creds, err := Bootstrap(ctx, cfg.Hash, cfg.ParticipantName)
	if err != nil {
		return nil, err
	}

	logger.Info("yandex: bootstrap ok; hash=", creds.Presentation)
	logger.Info("yandex: participant=", creds.Participant)

	boardCfg := DefaultBoardConfig()
	boardCfg.QueueSize = cfg.BoardQueue
	boardCfg.HeartbeatDisabled = cfg.HeartbeatDisabled

	client := NewBoardClient(logger, creds, boardCfg)
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}

	heartbeatCtx, _ := context.WithCancel(context.Background())
	client.StartPeriodicHeartbeat(heartbeatCtx)

	pipeCfg := DefaultPipeConfig()
	pipeCfg.QueueSize = cfg.PipeQueue
	pipeCfg.SendBatch = cfg.SendBatch
	pipeCfg.SendFlushMs = cfg.SendFlushMs
	pipeCfg.DeleteEnabled = cfg.EffectiveDeleteEnabled()
	pipeCfg.DeleteBatch = cfg.DeleteBatch
	pipeCfg.DeleteFlushMs = cfg.DeleteFlushMs
	pipeCfg.StatsEnabled = !cfg.StatsDisabled

	var pipe transport.Pipe = NewYandexPipe(client, logger, pipeCfg)

	if s.Advanced.PaddingEnabled {
		target := s.Advanced.PaddingTarget
		if target <= 0 {
			target = 14000
		}
		pp, err := security.NewPaddingPipe(pipe, target)
		if err != nil {
			return nil, err
		}
		pipe = pp
		logger.Info("yandex: padding enabled; target=", target)
	}

	if s.EncryptionPassword != "" {
		sp, err := security.NewSecurePipe(pipe, s.EncryptionPassword, logger)
		if err != nil {
			return nil, err
		}
		pipe = sp
		logger.Info("yandex: encryption enabled")
	}

	if s.Advanced.JitterMaxMs > 0 {
		minD := time.Duration(s.Advanced.JitterMinMs) * time.Millisecond
		maxD := time.Duration(s.Advanced.JitterMaxMs) * time.Millisecond
		pipe = security.NewJitterPipe(pipe, minD, maxD)
		logger.Info("yandex: jitter enabled")
	}

	session, err := transport.NewReliableSession(pipe, logger, transport.Config{
		Window:    s.Advanced.ReliableWindow,
		ChunkSize: s.Advanced.ReliableChunk,
	})
	if err != nil {
		return nil, err
	}

	bypass := []string{
		"boards.yandex.ru",
		"socket63.boards.yandex.ru",
	}

	var sides []app.SideService
	if role == "server" && cfg.EffectiveWorkerEnabled() {
		workerCfg := WorkerConfig{
			Threshold:    time.Duration(cfg.WorkerThresholdSec) * time.Second,
			ScanInterval: time.Duration(cfg.WorkerScanSec) * time.Second,
			BatchSize:    cfg.WorkerBatch,
			BatchPause:   time.Duration(cfg.WorkerBatchPauseMs) * time.Millisecond,
		}
		sides = append(sides, NewCleanupWorker(creds, logger, workerCfg))
	}

	return app.NewRuntime(session, bypass, sides), nil
}