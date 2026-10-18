package tunnel

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// maxQueuedBytes — жёсткий потолок на накопленную очередь непрочитанных
// данных в одном потоке. При превышении поток закрывается с явной ошибкой
// (см. deliverData) — это гораздо лучше, чем продолжать расти в память
// до OOM. При нормальной работе очередь почти всегда пуста.
const maxQueuedBytes = 64 * 1024 * 1024

type TunnelStream struct {
	mux      *TunnelMultiplexer
	id       string
	metadata map[string]any

	// Входящая очередь заменяет ограниченный канал recvCh.
	// Причина: старый вариант на chan с буфером 256 + select/default
	// МОЛЧА терял куски потока при перегрузке. Для TCP-семантики это
	// фатально: получатель (curl/TLS, HTTP/2, SOCKS5-клиент) видит
	// непрерывный поток, но в нём есть дыры — и ломается на парсинге.
	// Unbounded-очередь под mutex+condvar эту проблему устраняет.
	recvMu      sync.Mutex
	recvCond    *sync.Cond
	recvQueue   [][]byte
	recvBytes   int
	recvClosed  bool
	recvErr     error

	readBuf []byte

	closed    atomic.Bool
	closeOnce sync.Once
}

func newTunnelStream(mux *TunnelMultiplexer, id string, metadata map[string]any) *TunnelStream {
	s := &TunnelStream{
		mux:      mux,
		id:       id,
		metadata: metadata,
	}
	s.recvCond = sync.NewCond(&s.recvMu)
	return s
}

func (s *TunnelStream) ID() string               { return s.id }
func (s *TunnelStream) Metadata() map[string]any { return s.metadata }
func (s *TunnelStream) Closed() bool             { return s.closed.Load() }

func (s *TunnelStream) Read(b []byte) (int, error) {
	// Сначала опустошаем остаток предыдущего фрейма.
	if len(s.readBuf) > 0 {
		n := copy(b, s.readBuf)
		s.readBuf = s.readBuf[n:]
		return n, nil
	}

	s.recvMu.Lock()
	for len(s.recvQueue) == 0 && !s.recvClosed {
		s.recvCond.Wait()
	}
	if len(s.recvQueue) == 0 {
		err := s.recvErr
		s.recvMu.Unlock()
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	data := s.recvQueue[0]
	s.recvQueue[0] = nil // помочь GC
	s.recvQueue = s.recvQueue[1:]
	s.recvBytes -= len(data)
	s.recvMu.Unlock()

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
		s.recvMu.Lock()
		s.recvClosed = true
		if s.recvErr == nil {
			s.recvErr = io.EOF
		}
		s.recvCond.Broadcast()
		s.recvMu.Unlock()
		_ = s.mux.closeStream(s.id)
	})
	return nil
}

// deliverData вызывается из receiveLoop мультиплексора. Раньше был
// select/default и молча дропал данные при заполненном канале. Теперь
// кладём в unbounded-очередь и будим Read через condvar.
// Если очередь превысила maxQueuedBytes — закрываем поток с явной
// ошибкой, чтобы не уехать в OOM. Тогда получатель получит
// осмысленную ошибку, а не повреждённый поток.
func (s *TunnelStream) deliverData(data []byte) {
	s.recvMu.Lock()
	if s.recvClosed {
		s.recvMu.Unlock()
		return
	}
	if s.recvBytes+len(data) > maxQueuedBytes {
		s.recvClosed = true
		s.recvErr = errors.New("tunnel: receive queue overflow")
		s.recvCond.Broadcast()
		s.recvMu.Unlock()
		return
	}
	s.recvBytes += len(data)
	s.recvQueue = append(s.recvQueue, data)
	s.recvCond.Signal()
	s.recvMu.Unlock()
}

func (s *TunnelStream) deliverEOF(err error) {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.recvMu.Lock()
		s.recvClosed = true
		s.recvErr = err
		s.recvCond.Broadcast()
		s.recvMu.Unlock()
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