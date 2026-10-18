// FILE: internal/transport/yandex_telemost/session.go
package telemost

import (
	"context"
	"errors"
	"sync"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
	"github.com/Esteviredzu/shampoore-core/internal/transport/kcp"
	"github.com/Esteviredzu/shampoore-core/internal/transport/squares"
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
	SquaresCellSize int // ← NEW
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
	sqPipe   *squares.Pipe
	inner    transport.Transport
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

	sqCfg := squares.Config{
		Width:    s.opts.SquaresWidth,
		Height:   s.opts.SquaresHeight,
		Cols:     s.opts.SquaresCols,
		Rows:     s.opts.SquaresRows,
		CellSize: s.opts.SquaresCellSize, // ← NEW
		FPS:      s.opts.SquaresFPS,
		Bitrate:  s.opts.SquaresBitrate,
		Copies:   s.opts.SquaresCopies,
	}
	sqPipe, err := squares.NewPipe(tunnel, sqCfg)
	if err != nil {
		_ = pipe.Stop(ctx)
		return err
	}
	if s.opts.UseSharing {
		sqPipe.SetSharing(true)
		s.logger.Info("telemost: squares pipe in SHARING mode")
	}
	if err := sqPipe.Start(ctx); err != nil {
		_ = pipe.Stop(ctx)
		return err
	}
	s.logger.Info("telemost: squares pipe started; grid=",
		sqCfg.Cols, "x", sqCfg.Rows, " cell=", sqCfg.CellSize, "px",
		" res=", sqCfg.Width, "x", sqCfg.Height,
		" fps=", sqCfg.FPS,
		" copies=", sqCfg.Copies,
		" max_msg=", sqPipe.MaxMessageSize())

	kcpCfg := kcp.Config{
		Conv:     s.opts.KcpConv,
		MTU:      s.opts.KcpMTU,
		Window:   s.opts.KcpWindow,
		NoDelay:  s.opts.KcpNoDelay,
		Interval: s.opts.KcpInterval,
		Resend:   s.opts.KcpResend,
		NC:       s.opts.KcpNC,
	}
	inner, err := kcp.NewSession(sqPipe, s.logger, kcpCfg)
	if err != nil {
		_ = sqPipe.Stop(ctx)
		_ = pipe.Stop(ctx)
		return err
	}
	if err := inner.Start(ctx); err != nil {
		_ = sqPipe.Stop(ctx)
		_ = pipe.Stop(ctx)
		return err
	}

	s.mu.Lock()
	s.pipe = pipe
	s.sqPipe = sqPipe
	s.inner = inner
	s.started = true
	s.mu.Unlock()

	s.logger.Info("telemost: ready (squares + kcp, conv=", s.opts.KcpConv,
		" mtu=", s.opts.KcpMTU, " window=", s.opts.KcpWindow, ")")
	return nil
}

func (s *Session) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		inner := s.inner
		sqPipe := s.sqPipe
		pipe := s.pipe
		s.started = false
		s.mu.Unlock()

		if inner != nil {
			_ = inner.Stop(ctx)
		}
		if sqPipe != nil {
			_ = sqPipe.Stop(ctx)
		}
		if pipe != nil {
			_ = pipe.Stop(ctx)
		}
	})
	return nil
}

func (s *Session) Send(ctx context.Context, data []byte) error {
	s.mu.Lock()
	inner := s.inner
	s.mu.Unlock()
	if inner == nil {
		return errors.New("telemost: session not started")
	}
	return inner.Send(ctx, data)
}

func (s *Session) Receive(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	inner := s.inner
	s.mu.Unlock()
	if inner == nil {
		return nil, errors.New("telemost: session not started")
	}
	return inner.Receive(ctx)
}

var _ transport.Transport = (*Session)(nil)