package security

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"

	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

const paddingHeaderSize = 4

type PaddingPipe struct {
	inner       transport.Pipe
	payloadSize int
	usableSize  int
}

func NewPaddingPipe(inner transport.Pipe, targetSize int) (*PaddingPipe, error) {
	innerMax := inner.MaxMessageSize()
	if targetSize <= paddingHeaderSize {
		return nil, errors.New("padding-pipe: target too small")
	}
	if targetSize > innerMax {
		targetSize = innerMax
	}
	return &PaddingPipe{
		inner:       inner,
		payloadSize: targetSize,
		usableSize:  targetSize - paddingHeaderSize,
	}, nil
}

func (p *PaddingPipe) Start(ctx context.Context) error { return p.inner.Start(ctx) }
func (p *PaddingPipe) Stop(ctx context.Context) error  { return p.inner.Stop(ctx) }
func (p *PaddingPipe) SessionID() string               { return p.inner.SessionID() }
func (p *PaddingPipe) MaxMessageSize() int             { return p.usableSize }

func (p *PaddingPipe) SendMessage(ctx context.Context, data []byte, key string) error {
	if len(data) > p.usableSize {
		return errors.New("padding-pipe: payload too large")
	}
	buf := make([]byte, p.payloadSize)
	binary.BigEndian.PutUint32(buf[:paddingHeaderSize], uint32(len(data)))
	copy(buf[paddingHeaderSize:], data)
	if pad := p.usableSize - len(data); pad > 0 {
		_, _ = rand.Read(buf[paddingHeaderSize+len(data):])
	}
	return p.inner.SendMessage(ctx, buf, key)
}

func (p *PaddingPipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	for {
		buf, err := p.inner.ReceiveMessage(ctx)
		if err != nil {
			return nil, err
		}
		if len(buf) < paddingHeaderSize {
			continue
		}
		length := binary.BigEndian.Uint32(buf[:paddingHeaderSize])
		if int(length) > len(buf)-paddingHeaderSize {
			continue
		}
		return buf[paddingHeaderSize : paddingHeaderSize+int(length)], nil
	}
}

func (p *PaddingPipe) DeleteMessage(ctx context.Context, key string) error {
	return p.inner.DeleteMessage(ctx, key)
}