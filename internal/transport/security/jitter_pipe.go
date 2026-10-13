package security

import (
	"context"
	"math/rand"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

type JitterPipe struct {
	inner    transport.Pipe
	minDelay time.Duration
	maxDelay time.Duration
}

func NewJitterPipe(inner transport.Pipe, minDelay, maxDelay time.Duration) *JitterPipe {
	return &JitterPipe{inner: inner, minDelay: minDelay, maxDelay: maxDelay}
}

func (p *JitterPipe) Start(ctx context.Context) error { return p.inner.Start(ctx) }
func (p *JitterPipe) Stop(ctx context.Context) error  { return p.inner.Stop(ctx) }
func (p *JitterPipe) SessionID() string               { return p.inner.SessionID() }
func (p *JitterPipe) MaxMessageSize() int             { return p.inner.MaxMessageSize() }

func (p *JitterPipe) SendMessage(ctx context.Context, data []byte, key string) error {
	if p.maxDelay > 0 {
		d := p.minDelay
		if p.maxDelay > p.minDelay {
			d += time.Duration(rand.Int63n(int64(p.maxDelay - p.minDelay)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return p.inner.SendMessage(ctx, data, key)
}

func (p *JitterPipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	return p.inner.ReceiveMessage(ctx)
}

func (p *JitterPipe) DeleteMessage(ctx context.Context, key string) error {
	return p.inner.DeleteMessage(ctx, key)
}