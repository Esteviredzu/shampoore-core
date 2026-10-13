package tunnel

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type TunnelStream struct {
	mux      *TunnelMultiplexer
	id       string
	metadata map[string]any

	recvCh  chan []byte
	readBuf []byte

	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

func newTunnelStream(mux *TunnelMultiplexer, id string, metadata map[string]any) *TunnelStream {
	return &TunnelStream{
		mux:      mux,
		id:       id,
		metadata: metadata,
		recvCh:   make(chan []byte, 256),
	}
}

func (s *TunnelStream) ID() string               { return s.id }
func (s *TunnelStream) Metadata() map[string]any { return s.metadata }
func (s *TunnelStream) Closed() bool             { return s.closed.Load() }

func (s *TunnelStream) Read(b []byte) (int, error) {
	if len(s.readBuf) > 0 {
		n := copy(b, s.readBuf)
		s.readBuf = s.readBuf[n:]
		return n, nil
	}

	data, ok := <-s.recvCh
	if !ok {
		if s.closeErr != nil {
			return 0, s.closeErr
		}
		return 0, io.EOF
	}
	n := copy(b, data)
	if n < len(data) {
		s.readBuf = data[n:]
	}
	return n, nil
}

func (s *TunnelStream) Write(b []byte) (int, error) {
	if s.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if err := s.mux.writeData(s.id, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (s *TunnelStream) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		close(s.recvCh)
		_ = s.mux.closeStream(s.id)
	})
	return nil
}

func (s *TunnelStream) deliverData(data []byte) {
	if s.closed.Load() {
		return
	}
	select {
	case s.recvCh <- data:
	default:
	}
}

func (s *TunnelStream) deliverEOF(err error) {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.closeErr = err
		close(s.recvCh)
	})
}

func (s *TunnelStream) LocalAddr() net.Addr  { return dummyAddr("tunnel") }
func (s *TunnelStream) RemoteAddr() net.Addr { return dummyAddr("tunnel") }

func (s *TunnelStream) SetDeadline(t time.Time) error      { return nil }
func (s *TunnelStream) SetReadDeadline(t time.Time) error  { return nil }
func (s *TunnelStream) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr string

func (d dummyAddr) Network() string { return string(d) }
func (d dummyAddr) String() string  { return string(d) }