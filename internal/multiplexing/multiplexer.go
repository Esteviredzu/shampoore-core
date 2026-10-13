package multiplexing

import (
	"context"
	"net"
)

type Stream interface {
	net.Conn
	ID() string
	Metadata() map[string]any
	Closed() bool
}

type StreamMultiplexer interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	OpenStream(ctx context.Context, metadata map[string]any) (Stream, error)
	AcceptStream(ctx context.Context) (Stream, error)
}