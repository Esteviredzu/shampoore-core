package vp8

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"

	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

const defaultMaxMessageSize = 16000

type PipeOptions struct {
	SessionID  string
	Obfuscator *Obfuscator
	// SendDelayMs — задержка между отправкой VP8-сэмплов.
	// 0 = без задержки. Для Telemost имеет смысл 10-20ms.
	SendDelayMs int
}

type Pipe struct {
	tunnel    *Tunnel
	obf       *Obfuscator
	sessionID string

	sendDelay time.Duration

	sendCount atomic.Uint64
	recvCount atomic.Uint64

	stopOnce sync.Once
}

func NewPipe(pubPC, subPC *webrtc.PeerConnection, opts PipeOptions) (*Pipe, error) {
	tunnel, err := NewTunnel(pubPC, subPC)
	if err != nil {
		return nil, err
	}
	return AttachPipe(tunnel, opts), nil
}

// AttachPipe — оборачивает уже готовый tunnel (например, созданный signaling'ом).
func AttachPipe(tunnel *Tunnel, opts PipeOptions) *Pipe {
	sid := opts.SessionID
	if sid == "" {
		sid = uuid.NewString()
	}

	obf := opts.Obfuscator
	if obf == nil {
		obf = NewObfuscator("")
	}

	var delay time.Duration
	if opts.SendDelayMs > 0 {
		delay = time.Duration(opts.SendDelayMs) * time.Millisecond
	}

	return &Pipe{
		tunnel:    tunnel,
		obf:       obf,
		sessionID: sid,
		sendDelay: delay,
	}
}

func (p *Pipe) Start(_ context.Context) error { return nil }

func (p *Pipe) Stop(_ context.Context) error {
	var err error
	p.stopOnce.Do(func() { err = p.tunnel.Close() })
	return err
}

func (p *Pipe) SendMessage(ctx context.Context, data []byte, _ string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if p.sendDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.sendDelay):
		}
	}

	n := p.sendCount.Add(1)
	if n <= 20 || n%100 == 0 {
		log.Printf("[vp8-pipe] send #%d size=%d", n, len(data))
	}
	return p.tunnel.WriteSample(p.obf.Obfuscate(data))
}

func (p *Pipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	raw, err := p.tunnel.Receive(ctx)
	if err != nil {
		return nil, err
	}
	n := p.recvCount.Add(1)
	if n <= 20 || n%100 == 0 {
		log.Printf("[vp8-pipe] recv #%d size=%d", n, len(raw))
	}
	return p.obf.Deobfuscate(raw), nil
}

func (p *Pipe) DeleteMessage(_ context.Context, _ string) error { return nil }

func (p *Pipe) SessionID() string   { return p.sessionID }
func (p *Pipe) MaxMessageSize() int { return defaultMaxMessageSize }

var _ transport.Pipe = (*Pipe)(nil)