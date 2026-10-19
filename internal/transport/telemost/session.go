package telemost

import (
	"context"
	"errors"
	"sync"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/core/channel/vp8"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

type SessionOptions struct {
	KcpConv     uint32
	KcpWindow   int
	KcpMTU      int
	KcpNoDelay  int
	KcpInterval int
	KcpResend   int
	KcpNC       bool

	SquaresWidth    int
	SquaresHeight   int
	SquaresCols     int
	SquaresRows     int
	SquaresCellSize int
	SquaresFPS      int
	SquaresBitrate  string
	SquaresCopies   int

	UseSharing bool
}

type Session struct {
	cfg    *Config
	boot   *bootstrapResult
	logger logging.ContextLogger
	opts   SessionOptions

	mu       sync.Mutex
	pipe     *Pipe
	tunnel   *vp8.Tunnel
	started  bool
	stopOnce sync.Once
}

func NewSession(
	cfg *Config,
	boot *bootstrapResult,
	logger logging.ContextLogger,
	opts SessionOptions,
) *Session {
	return &Session{cfg: cfg, boot: boot, logger: logger, opts: opts}
}

func (s *Session) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if s.opts.UseSharing {
		s.cfg.UseSharing = true
	}

	pipe := NewPipe(s.cfg, s.boot, s.logger)
	if err := pipe.Start(ctx); err != nil {
		return err
	}
	tunnel := pipe.Tunnel()
	if tunnel == nil {
		_ = pipe.Stop(ctx)
		return errors.New("telemost: vp8 tunnel not created")
	}
	tunnel.SetRawMode(true)
	s.logger.Info("telemost: vp8 tunnel attached (raw mode)")

	s.mu.Lock()
	s.pipe = pipe
	s.tunnel = tunnel
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *Session) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		pipe := s.pipe
		s.started = false
		s.mu.Unlock()
		if pipe != nil {
			_ = pipe.Stop(ctx)
		}
	})
	return nil
}

func (s *Session) Tunnel() *vp8.Tunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tunnel
}

var _ app.Provider = (*Session)(nil)