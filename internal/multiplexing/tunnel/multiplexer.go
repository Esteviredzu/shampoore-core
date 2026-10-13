package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

type Config struct {
	FlushThreshold int
	FlushInterval  time.Duration
}

func DefaultConfig() Config {
	return Config{
		FlushThreshold: 12000,
		FlushInterval:  20 * time.Millisecond,
	}
}

type TunnelMultiplexer struct {
	transport transport.Transport
	logger    logging.ContextLogger
	cfg       Config

	mu      sync.Mutex
	streams map[string]*TunnelStream
	closed  bool

	incoming chan multiplexing.Stream

	outboundMu     sync.Mutex
	outboundBuf    []byte
	outboundNotify chan struct{}

	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	wg      sync.WaitGroup
}

func New(
	t transport.Transport,
	logger logging.ContextLogger,
	cfg Config,
) *TunnelMultiplexer {
	if cfg.FlushThreshold <= 0 {
		cfg.FlushThreshold = 12000
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 20 * time.Millisecond
	}
	return &TunnelMultiplexer{
		transport:      t,
		logger:         logger,
		cfg:            cfg,
		streams:        make(map[string]*TunnelStream),
		incoming:       make(chan multiplexing.Stream, 64),
		outboundNotify: make(chan struct{}, 1),
	}
}

func (m *TunnelMultiplexer) Start(ctx context.Context) error {
	if m.started {
		return nil
	}
	if err := m.transport.Start(ctx); err != nil {
		return err
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true

	m.wg.Add(2)
	go m.receiveLoop()
	go m.flushLoop()
	return nil
}

func (m *TunnelMultiplexer) Stop(ctx context.Context) error {
	if !m.started {
		return nil
	}
	m.started = false

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	streams := make([]*TunnelStream, 0, len(m.streams))
	for _, s := range m.streams {
		streams = append(streams, s)
	}
	m.streams = make(map[string]*TunnelStream)
	m.mu.Unlock()

	m.cancel()
	m.flushOutbound(context.Background())

	for _, s := range streams {
		s.deliverEOF(io.EOF)
	}
	m.wg.Wait()

	return m.transport.Stop(ctx)
}

func (m *TunnelMultiplexer) OpenStream(
	ctx context.Context,
	metadata map[string]any,
) (multiplexing.Stream, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("tunnel: multiplexer is closed")
	}
	id := uuid.NewString()
	stream := newTunnelStream(m, id, metadata)
	m.streams[id] = stream
	m.mu.Unlock()

	frame := &TunnelFrame{
		StreamID: id,
		Type:     FrameOpen,
		Metadata: metadata,
	}
	if err := m.enqueueFrame(ctx, frame); err != nil {
		m.mu.Lock()
		delete(m.streams, id)
		m.mu.Unlock()
		return nil, err
	}
	m.flushOutbound(ctx)
	return stream, nil
}

func (m *TunnelMultiplexer) AcceptStream(
	ctx context.Context,
) (multiplexing.Stream, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case s, ok := <-m.incoming:
		if !ok {
			return nil, io.EOF
		}
		return s, nil
	}
}

func (m *TunnelMultiplexer) writeData(id string, data []byte) error {
	m.mu.Lock()
	_, ok := m.streams[id]
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return io.ErrClosedPipe
	}
	if !ok {
		return fmt.Errorf("tunnel: stream not found: %s", id)
	}
	return m.enqueueFrame(m.ctx, &TunnelFrame{
		StreamID: id,
		Type:     FrameData,
		Data:     data,
	})
}

func (m *TunnelMultiplexer) closeStream(id string) error {
	m.mu.Lock()
	_, ok := m.streams[id]
	if ok {
		delete(m.streams, id)
	}
	closed := m.closed
	m.mu.Unlock()

	if !ok || closed {
		return nil
	}
	return m.enqueueFrame(m.ctx, &TunnelFrame{
		StreamID: id,
		Type:     FrameClose,
	})
}

func (m *TunnelMultiplexer) enqueueFrame(ctx context.Context, f *TunnelFrame) error {
	encoded, err := EncodeFrame(f)
	if err != nil {
		return err
	}

	m.outboundMu.Lock()
	m.outboundBuf = append(m.outboundBuf, encoded...)
	size := len(m.outboundBuf)
	m.outboundMu.Unlock()

	if size >= m.cfg.FlushThreshold {
		m.flushOutbound(ctx)
	} else {
		select {
		case m.outboundNotify <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *TunnelMultiplexer) flushOutbound(ctx context.Context) {
	m.outboundMu.Lock()
	if len(m.outboundBuf) == 0 {
		m.outboundMu.Unlock()
		return
	}
	buf := m.outboundBuf
	m.outboundBuf = nil
	m.outboundMu.Unlock()

	if err := m.transport.Send(ctx, buf); err != nil {
		m.logger.Warn("tunnel: flush error (", len(buf), " bytes): ", err)
	}
}

func (m *TunnelMultiplexer) flushLoop() {
	defer m.wg.Done()

	timer := time.NewTimer(m.cfg.FlushInterval)
	defer timer.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.outboundNotify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(m.cfg.FlushInterval)
		case <-timer.C:
			m.flushOutbound(m.ctx)
			timer.Reset(m.cfg.FlushInterval)
		}
	}
}

func (m *TunnelMultiplexer) receiveLoop() {
	defer m.wg.Done()
	decoder := NewFrameDecoder()

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		data, err := m.transport.Receive(m.ctx)
		if err != nil {
			if m.ctx.Err() != nil {
				return
			}
			if errors.Is(err, transport.ErrTransportReset) {
				m.logger.Warn("tunnel: transport reset; closing all streams")
				decoder.Reset()
				m.closeAllStreams(transport.ErrTransportReset)
				continue
			}
			if errors.Is(err, io.EOF) {
				m.logger.Info("tunnel: transport EOF")
				m.closeAllStreams(io.EOF)
				return
			}
			m.logger.Error("tunnel: receive error: ", err)
			m.closeAllStreams(err)
			return
		}

		frames, err := decoder.Feed(data)
		if err != nil {
			m.logger.Warn("tunnel: decode error: ", err)
			decoder.Reset()
			continue
		}

		for _, f := range frames {
			m.handleFrame(f)
		}
	}
}

func (m *TunnelMultiplexer) handleFrame(f *TunnelFrame) {
	switch f.Type {
	case FrameOpen:
		m.mu.Lock()
		if _, exists := m.streams[f.StreamID]; exists {
			m.mu.Unlock()
			return
		}
		stream := newTunnelStream(m, f.StreamID, f.Metadata)
		m.streams[f.StreamID] = stream
		closed := m.closed
		m.mu.Unlock()

		if closed {
			stream.deliverEOF(io.EOF)
			return
		}
		select {
		case m.incoming <- stream:
		case <-m.ctx.Done():
			stream.deliverEOF(io.EOF)
		}

	case FrameData:
		m.mu.Lock()
		stream := m.streams[f.StreamID]
		m.mu.Unlock()
		if stream != nil {
			stream.deliverData(f.Data)
		}

	case FrameClose:
		m.mu.Lock()
		stream := m.streams[f.StreamID]
		delete(m.streams, f.StreamID)
		m.mu.Unlock()
		if stream != nil {
			stream.deliverEOF(io.EOF)
		}
	}
}

func (m *TunnelMultiplexer) closeAllStreams(err error) {
	m.mu.Lock()
	streams := make([]*TunnelStream, 0, len(m.streams))
	for _, s := range m.streams {
		streams = append(streams, s)
	}
	m.streams = make(map[string]*TunnelStream)
	m.mu.Unlock()

	for _, s := range streams {
		s.deliverEOF(err)
	}
}