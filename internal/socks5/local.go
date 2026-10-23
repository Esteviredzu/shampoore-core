package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

const (
	socksVersion = 0x05

	methodNoAuth   = 0x00
	methodNoAccept = 0xFF

	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	repSuccess             = 0x00
	repGeneralFailure      = 0x01
	repHostUnreachable     = 0x04
	repCommandNotSupported = 0x07
	repAddressNotSupported = 0x08
)

type LocalServer struct {
	listen   string
	mux      multiplexing.StreamMultiplexer
	logger   logging.ContextLogger
	ipv4Only bool
	ln       net.Listener
}

func NewLocalServer(listen string, m multiplexing.StreamMultiplexer, log logging.ContextLogger, ipv4Only bool) *LocalServer {
	return &LocalServer{
		listen:   listen,
		mux:      m,
		logger:   log,
		ipv4Only: ipv4Only,
	}
}

func (s *LocalServer) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	s.ln = ln
	s.logger.Info("socks5: listening on ", s.listen)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *LocalServer) Stop() {
	if s.ln != nil {
		s.ln.Close()
	}
}

func (s *LocalServer) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	if err := s.negotiate(conn); err != nil {
		s.logger.Warn("socks5: negotiate: ", err)
		return
	}

	host, port, err := s.readRequest(conn)
	if err != nil {
		s.logger.Warn("socks5: request: ", err)
		return
	}

	if s.ipv4Only {
		ip := net.ParseIP(host)
		switch {
		case ip == nil:
			resolved, err := resolveIPv4(host)
			if err != nil {
				s.logger.Warn("socks5: resolve ", host, ": ", err)
				s.sendReply(conn, repHostUnreachable)
				return
			}
			host = resolved
		case ip.To4() == nil:
			s.logger.Warn("socks5: ipv6 destination rejected: ", host)
			s.sendReply(conn, repAddressNotSupported)
			return
		}
	}

	stream, err := s.mux.OpenStream(ctx, map[string]any{
		"host": host,
		"port": port,
	})
	if err != nil {
		s.logger.Warn("socks5: open stream: ", err)
		s.sendReply(conn, repGeneralFailure)
		return
	}
	defer stream.Close()

	if err := s.sendReply(conn, repSuccess); err != nil {
		return
	}

	// Контекст-разблокировка: при отмене ctx закрываем conn и stream,
	// чтобы io.Copy не висел.
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
			stream.Close()
		case <-stopWatch:
		}
	}()
	defer close(stopWatch)

	done := make(chan struct{}, 2)
	go func() { io.Copy(stream, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, stream); done <- struct{}{} }()
	<-done
}

func (s *LocalServer) negotiate(conn net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != socksVersion {
		return errors.New("bad version")
	}
	nmethods := int(header[1])
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	for _, m := range methods {
		if m == methodNoAuth {
			_, err := conn.Write([]byte{socksVersion, methodNoAuth})
			return err
		}
	}
	conn.Write([]byte{socksVersion, methodNoAccept})
	return errors.New("no auth method")
}

func (s *LocalServer) readRequest(conn net.Conn) (string, int, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", 0, err
	}
	if head[0] != socksVersion {
		return "", 0, errors.New("bad version")
	}
	if head[1] != cmdConnect {
		s.sendReply(conn, repCommandNotSupported)
		return "", 0, errors.New("only CONNECT supported")
	}

	var host string
	switch head[3] {
	case atypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	case atypDomain:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(conn, lb); err != nil {
			return "", 0, err
		}
		domain := make([]byte, int(lb[0]))
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", 0, err
		}
		host = string(domain)
	case atypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", 0, err
		}
		host = net.IP(buf).String()
	default:
		s.sendReply(conn, repAddressNotSupported)
		return "", 0, errors.New("bad atyp")
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", 0, err
	}
	port := int(binary.BigEndian.Uint16(portBuf))

	return host, port, nil
}

func (s *LocalServer) sendReply(conn net.Conn, code byte) error {
	_, err := conn.Write([]byte{
		socksVersion, code, 0x00, atypIPv4,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
	})
	return err
}

func isIP(s string) bool {
	return net.ParseIP(s) != nil
}

func resolveIPv4(host string) (string, error) {
	addrs, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 for %s", host)
}