package muxproxy

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"

	M "github.com/xjasonlyu/tun2socks/v2/metadata"
	"github.com/xjasonlyu/tun2socks/v2/proxy"

	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

var defaultMux multiplexing.StreamMultiplexer

func SetMux(m multiplexing.StreamMultiplexer) {
	defaultMux = m
}

type MuxProxy struct{}

func (p *MuxProxy) DialContext(ctx context.Context, meta *M.Metadata) (net.Conn, error) {
	if defaultMux == nil {
		return nil, fmt.Errorf("muxproxy: mux not initialized")
	}

	addr := meta.DestinationAddress()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("muxproxy: split host:port: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("muxproxy: parse port: %w", err)
	}

	stream, err := defaultMux.OpenStream(ctx, map[string]any{
		"host": host,
		"port": port,
	})
	if err != nil {
		return nil, fmt.Errorf("muxproxy: open stream: %w", err)
	}
	return stream, nil
}

func (p *MuxProxy) DialUDP(*M.Metadata) (net.PacketConn, error) {
	return nil, fmt.Errorf("muxproxy: UDP not supported")
}

func Parse(_ *url.URL) (proxy.Proxy, error) {
	return &MuxProxy{}, nil
}

func init() {
	proxy.RegisterProtocol("mux", Parse)
}