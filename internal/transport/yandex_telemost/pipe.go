package telemost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
	"github.com/Esteviredzu/shampoore-core/internal/transport/vp8"
)

const (
	publishTimeout   = 45 * time.Second
	subscribeTimeout = 30 * time.Second
)

type Pipe struct {
	cfg        *Config
	boot       *bootstrapResult
	logger     logging.ContextLogger
	vp8Options vp8.PipeOptions

	sig   *signaling
	inner transport.Pipe

	started  bool
	stopOnce sync.Once
}

func NewPipe(
	cfg *Config,
	boot *bootstrapResult,
	logger logging.ContextLogger,
	vp8Opts vp8.PipeOptions,
) *Pipe {
	return &Pipe{
		cfg:        cfg,
		boot:       boot,
		logger:     logger,
		vp8Options: vp8Opts,
	}
}

func (p *Pipe) Start(ctx context.Context) error {
	if p.started {
		return nil
	}

	p.sig = newSignaling(p.cfg, p.boot, p.logger)

	pubReady := make(chan struct{})
	subReady := make(chan struct{})
	var pubOnce, subOnce sync.Once
	p.sig.onPublishReady = func() { pubOnce.Do(func() { close(pubReady) }) }
	p.sig.onSubscribe = func() { subOnce.Do(func() { close(subReady) }) }

	runErr := make(chan error, 1)
	go func() {
		runErr <- p.sig.Run(ctx)
	}()

	p.logger.Info("telemost: waiting for publish signaling...")
	select {
	case <-pubReady:
	case err := <-runErr:
		return fmt.Errorf("telemost: signaling stopped: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(publishTimeout):
		return errors.New("telemost: publish signaling timeout")
	}
	p.logger.Info("telemost: publish ready")

	p.logger.Info("telemost: waiting for subscribe signaling...")
	select {
	case <-subReady:
		p.logger.Info("telemost: subscribe ready")
	case err := <-runErr:
		return fmt.Errorf("telemost: signaling stopped: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(subscribeTimeout):
		p.logger.Warn("telemost: no subscriber yet, continuing " +
			"(will attach when peer joins)")
	}

	tunnel := p.sig.Tunnel()
	if tunnel == nil {
		return errors.New("telemost: tunnel not created")
	}
	p.inner = vp8.AttachPipe(tunnel, p.vp8Options)
	if err := p.inner.Start(ctx); err != nil {
		return err
	}
	p.started = true
	return nil
}

func (p *Pipe) Stop(ctx context.Context) error {
	var err error
	p.stopOnce.Do(func() {
		if p.inner != nil {
			_ = p.inner.Stop(ctx)
		}
		if p.sig != nil {
			p.sig.Close()
		}
	})
	return err
}

func (p *Pipe) SendMessage(ctx context.Context, data []byte, key string) error {
	if p.inner == nil {
		return errors.New("telemost: pipe not started")
	}
	return p.inner.SendMessage(ctx, data, key)
}

func (p *Pipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	if p.inner == nil {
		return nil, errors.New("telemost: pipe not started")
	}
	return p.inner.ReceiveMessage(ctx)
}

func (p *Pipe) DeleteMessage(ctx context.Context, key string) error {
	if p.inner == nil {
		return nil
	}
	return p.inner.DeleteMessage(ctx, key)
}

func (p *Pipe) SessionID() string {
	if p.inner == nil {
		return ""
	}
	return p.inner.SessionID()
}

func (p *Pipe) MaxMessageSize() int {
	if p.inner == nil {
		return 16000
	}
	return p.inner.MaxMessageSize()
}

var _ transport.Pipe = (*Pipe)(nil)



func (p *Pipe) Tunnel() *vp8.Tunnel {
	if p.sig == nil {
		return nil
	}
	return p.sig.Tunnel()
}