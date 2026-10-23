package telemost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/core/channel/vp8"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

const (
	publishTimeout   = 45 * time.Second
	subscribeTimeout = 30 * time.Second
)

type Pipe struct {
	cfg    *Config
	boot   *bootstrapResult
	logger logging.ContextLogger

	sig    *signaling
	tunnel *vp8.Tunnel

	started  bool
	stopOnce sync.Once
}

func NewPipe(cfg *Config, boot *bootstrapResult, logger logging.ContextLogger) *Pipe {
	return &Pipe{cfg: cfg, boot: boot, logger: logger}
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
	go func() { runErr <- p.sig.Run(ctx) }()

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
		p.logger.Warn("telemost: no subscriber yet, continuing")
	}

	tunnel := p.sig.Tunnel()
	if tunnel == nil {
		return errors.New("telemost: tunnel not created")
	}
	p.tunnel = tunnel
	p.started = true
	return nil
}

func (p *Pipe) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		if p.sig != nil {
			p.sig.Close()
		}
	})
	return nil
}

func (p *Pipe) Tunnel() *vp8.Tunnel {
	if p.sig == nil {
		return nil
	}
	return p.sig.Tunnel()
}