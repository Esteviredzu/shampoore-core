// FILE: internal/transport/kcp/session.go
package kcp

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	kcp "github.com/xtaci/kcp-go/v5"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

type Config struct {
	Conv     uint32
	MTU      int
	Window   int
	NoDelay  int
	Interval int
	Resend   int
	NC       bool
}

func DefaultConfig() Config {
	return Config{
		Window:   64,
		NoDelay:  1,
		Interval: 10,
		Resend:   2,
		NC:       true,
	}
}

type Session struct {
	pipe   transport.Pipe
	cfg    Config
	logger logging.ContextLogger

	conv uint32
	kcp  *kcp.KCP

	outCount int

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool

	mu     sync.Mutex
	closed bool
}

func NewSession(
	pipe transport.Pipe,
	logger logging.ContextLogger,
	cfg Config,
) (*Session, error) {
	if cfg.Window <= 0 {
		cfg.Window = 64
	}
	if cfg.Conv == 0 {
		cfg.Conv = 1
	}
	if cfg.MTU <= 0 {
		max := pipe.MaxMessageSize()
		cfg.MTU = max - 24
		if cfg.MTU < 64 {
			return nil, errors.New("kcp: MTU too small")
		}
	}
	const maxKCPMTU = 1200
	if cfg.MTU > maxKCPMTU {
		cfg.MTU = maxKCPMTU
	}

	s := &Session{
		pipe:   pipe,
		cfg:    cfg,
		logger: logger,
		conv:   cfg.Conv,
	}

	k := kcp.NewKCP(s.conv, s.output)
	if k == nil {
		return nil, errors.New("kcp: failed to create KCP instance")
	}
	k.SetMtu(cfg.MTU)
	k.WndSize(cfg.Window, cfg.Window)
	k.NoDelay(cfg.NoDelay, cfg.Interval, cfg.Resend, boolToInt(cfg.NC))
	s.kcp = k

	return s, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Session) output(buf []byte, size int) {
	data := make([]byte, size)
	copy(data, buf[:size])
	key := uuid.NewString()

	s.mu.Lock()
	s.outCount++
	n := s.outCount
	s.mu.Unlock()
	if n <= 20 || n%100 == 0 {
		log.Printf("[kcp] output #%d size=%d", n, size)
	}

	if err := s.pipe.SendMessage(s.ctx, data, key); err != nil {
		s.logger.Warn("kcp: output send error: ", err)
	}
}

func trimPadding(data []byte) []byte {
	const kcpOverhead = 24
	if len(data) < kcpOverhead {
		return data
	}
	payloadLen := int(binary.LittleEndian.Uint32(data[20:24]))
	total := kcpOverhead + payloadLen
	if total >= kcpOverhead && total <= len(data) {
		return data[:total]
	}
	return data
}

func (s *Session) Start(ctx context.Context) error {
	if s.started {
		return nil
	}
	if err := s.pipe.Start(ctx); err != nil {
		return err
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.started = true

	s.logger.Info("kcp: started; conv=", s.conv,
		" mtu=", s.cfg.MTU,
		" window=", s.cfg.Window)

	s.wg.Add(2)
	go s.recvLoop()
	go s.updateLoop()
	return nil
}

func (s *Session) recvLoop() {
	defer s.wg.Done()
	inCount := 0
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		data, err := s.pipe.ReceiveMessage(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			s.logger.Warn("kcp: recv error: ", err)
			return
		}

		inCount++
		if inCount <= 20 || inCount%100 == 0 {
			log.Printf("[kcp] input #%d size=%d", inCount, len(data))
		}

		seg := trimPadding(data)
		s.kcp.Input(seg, kcp.IKCP_PACKET_REGULAR, false)
	}
}

func (s *Session) updateLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.kcp.Update()
		}
	}
}

func (s *Session) Send(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if ret := s.kcp.Send(data); ret < 0 {
		return errors.New("kcp: send failed")
	}
	return nil
}

func (s *Session) Receive(ctx context.Context) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return nil, io.EOF
		}

		size := s.kcp.PeekSize()
		if size < 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
			continue
		}

		buf := make([]byte, size)
		n := s.kcp.Recv(buf)
		if n < 0 {
			if n == -2 {
				buf = make([]byte, size+1024)
				n = s.kcp.Recv(buf)
			}
			if n < 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
				continue
			}
		}

		out := make([]byte, n)
		copy(out, buf[:n])
		return out, nil
	}
}

func (s *Session) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return s.pipe.Stop(ctx)
}

var _ transport.Transport = (*Session)(nil)