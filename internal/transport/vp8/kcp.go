package vp8

import (
	"context"
	"sync"
	"time"
)

type KCPPipe struct {
	tunnel    *Tunnel
	sendDelay time.Duration
	sessionID string

	stopOnce sync.Once
}

func NewKCPPipe(tunnel *Tunnel, sendDelayMs int) *KCPPipe {
	var d time.Duration
	if sendDelayMs > 0 {
		d = time.Duration(sendDelayMs) * time.Millisecond
	}
	return &KCPPipe{
		tunnel:    tunnel,
		sendDelay: d,
		sessionID: "vp8-kcp",
	}
}

func (k *KCPPipe) Start(_ context.Context) error { return nil }

func (k *KCPPipe) Stop(_ context.Context) error {
	var err error
	k.stopOnce.Do(func() { err = k.tunnel.Close() })
	return err
}

func (k *KCPPipe) SendMessage(ctx context.Context, data []byte, _ string) error {
	// Pacing только для data-чанков. ACK'и (67 байт) идут без паузы,
	// иначе они застревают в очереди SFU на секунды.
	if k.sendDelay > 0 && len(data) > 200 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(k.sendDelay):
		}
	}
	return k.tunnel.WriteSample(data)
}

func (k *KCPPipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	return k.tunnel.Receive(ctx)
}

func (k *KCPPipe) DeleteMessage(_ context.Context, _ string) error { return nil }

func (k *KCPPipe) SessionID() string   { return k.sessionID }
func (k *KCPPipe) MaxMessageSize() int { return 16000 }