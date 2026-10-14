package telemost

import (
	"context"
	"errors"
	"sync"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
	"github.com/Esteviredzu/shampoore-core/internal/transport/vp8"
)

type SessionOptions struct {
	VP8SendDelayMs int
	ReliableWindow int
	ReliableChunk  int
}

type Session struct {
	cfg    *Config
	boot   *bootstrapResult
	logger logging.ContextLogger
	opts   SessionOptions

	mu      sync.Mutex
	pipe    *Pipe
	inner   transport.Transport
	started bool

	stopOnce sync.Once
}

func NewSession(
	cfg *Config,
	boot *bootstrapResult,
	logger logging.ContextLogger,
	opts SessionOptions,
) *Session {
	return &Session{
		cfg:    cfg,
		boot:   boot,
		logger: logger,
		opts:   opts,
	}
}

func (s *Session) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	pipe := NewPipe(s.cfg, s.boot, s.logger, vp8.PipeOptions{SendDelayMs: 0})

	if err := pipe.Start(ctx); err != nil {
		return err
	}

	tunnel := pipe.Tunnel()
	if tunnel == nil {
		_ = pipe.Stop(ctx)
		return errors.New("telemost: vp8 tunnel not created")
	}
	s.logger.Info("telemost: vp8 tunnel attached")

	kcp := vp8.NewKCPPipe(tunnel, s.opts.VP8SendDelayMs)

	inner, err := transport.NewReliableSession(kcp, s.logger, transport.Config{
		Window:    s.opts.ReliableWindow,
		ChunkSize: s.opts.ReliableChunk,
	})
	if err != nil {
		_ = pipe.Stop(ctx)
		return err
	}

	if err := inner.Start(ctx); err != nil {
		_ = pipe.Stop(ctx)
		return err
	}

	s.mu.Lock()
	s.pipe = pipe
	s.inner = inner
	s.started = true
	s.mu.Unlock()

	return nil
}

func (s *Session) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() {
		s.mu.Lock()
		inner := s.inner
		pipe := s.pipe
		s.started = false
		s.mu.Unlock()

		if inner != nil {
			_ = inner.Stop(ctx)
		}
		if pipe != nil {
			_ = pipe.Stop(ctx)
		}
	})
	return err
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