package socks5

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

func dialTCP(target string, ipv4Only bool) (net.Conn, error) {
	network := "tcp"
	if ipv4Only {
		network = "tcp4"
	}
	return net.DialTimeout(network, target, 30*time.Second)
}

type RemoteHandler struct {
	mux       multiplexing.StreamMultiplexer
	logger    logging.ContextLogger
	chunkSize int
	ipv4Only  bool

	mu      sync.Mutex
	streams map[multiplexing.Stream]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRemoteHandler(
	ctx context.Context,
	m multiplexing.StreamMultiplexer,
	logger logging.ContextLogger,
	chunkSize int,
	ipv4Only bool,
) *RemoteHandler {
	if chunkSize <= 0 {
		chunkSize = 8000
	}
	sctx, cancel := context.WithCancel(ctx)
	return &RemoteHandler{
		mux:       m,
		logger:    logger,
		chunkSize: chunkSize,
		ipv4Only:  ipv4Only,
		streams:   make(map[multiplexing.Stream]struct{}),
		ctx:       sctx,
		cancel:    cancel,
	}
}

func (s *RemoteHandler) Run() error {
	s.logger.Info("remote: waiting for incoming streams...")

	for {
		stream, err := s.mux.AcceptStream(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return err
		}

		s.mu.Lock()
		s.streams[stream] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go func(st multiplexing.Stream) {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.streams, st)
				s.mu.Unlock()
			}()
			s.handleStream(st)
		}(stream)
	}
}

func (s *RemoteHandler) Stop() {
	s.cancel()
	s.mu.Lock()
	for st := range s.streams {
		st.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *RemoteHandler) handleStream(stream multiplexing.Stream) {
	streamID := stream.ID()
	meta := stream.Metadata()

	host, _ := meta["host"].(string)

	var port int
	switch v := meta["port"].(type) {
	case float64:
		port = int(v)
	case int:
		port = v
	case int64:
		port = int(v)
	}

	if host == "" || port == 0 {
		s.logger.Warn("remote: ", streamID, " invalid metadata")
		stream.Close()
		return
	}

	if s.ipv4Only {
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			s.logger.Warn("remote: ", streamID, " ipv6 destination rejected: ", host)
			stream.Close()
			return
		}
	}

	target := net.JoinHostPort(host, strconv.Itoa(port))
	s.logger.Info("remote: ", streamID, " -> ", target)

	conn, err := dialTCP(target, s.ipv4Only)
	if err != nil {
		s.logger.Warn("remote: ", streamID, " connect failed: ", err)
		stream.Close()
		return
	}

	s.logger.Debug("remote: ", streamID, " connected")

	defer func() {
		conn.Close()
		stream.Close()
		s.logger.Debug("remote: ", streamID, " closed")
	}()

	// Разблокировка conn.Read при отмене контекста (Stop).
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-s.ctx.Done():
			conn.Close()
		case <-stopWatch:
		}
	}()
	defer close(stopWatch)

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, s.chunkSize)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := stream.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, s.chunkSize)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	<-done
}