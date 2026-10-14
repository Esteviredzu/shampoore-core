package vp8

import (
	"context"
	"sync"
	"time"
)

// KCPPipe — прокси поверх VP8-туннеля. Настоящий KCP здесь НЕ используется,
// несмотря на название. Внутри: 4-байтовый BE префикс длины + pacing.
type KCPPipe struct {
	tunnel    *Tunnel
	sendDelay time.Duration

	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewKCPPipe(tunnel *Tunnel, sendDelayMs int) *KCPPipe {
	var d time.Duration
	if sendDelayMs > 0 {
		d = time.Duration(sendDelayMs) * time.Millisecond
	}
	return &KCPPipe{tunnel: tunnel, sendDelay: d}
}

func (k *KCPPipe) Start(ctx context.Context) error {
	k.mu.Lock()
	if k.ctx != nil {
		k.mu.Unlock()
		return nil
	}
	k.ctx, k.cancel = context.WithCancel(ctx)
	k.mu.Unlock()
	return nil
}

func (k *KCPPipe) Stop(ctx context.Context) error {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return nil
	}
	k.closed = true
	cancel := k.cancel
	k.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	k.wg.Wait()
	return nil
}

func (k *KCPPipe) Send(ctx context.Context, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if k.sendDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(k.sendDelay):
		}
	}

	packet := make([]byte, 4+len(data))
	packet[0] = byte(len(data) >> 24)
	packet[1] = byte(len(data) >> 16)
	packet[2] = byte(len(data) >> 8)
	packet[3] = byte(len(data))
	copy(packet[4:], data)

	return k.tunnel.WriteSample(packet)
}

func (k *KCPPipe) Receive(ctx context.Context) ([]byte, error) {
	for {
		raw, err := k.tunnel.Receive(ctx)
		if err != nil {
			return nil, err
		}
		if len(raw) < 4 {
			continue
		}

		length := int(raw[0])<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3])
		if length <= 0 || length > len(raw)-4 {
			continue
		}

		out := make([]byte, length)
		copy(out, raw[4:4+length])
		return out, nil
	}
}