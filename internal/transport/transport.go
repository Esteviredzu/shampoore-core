package transport

import (
	"context"
	"errors"
)

var ErrTransportReset = errors.New("transport reset")

type Transport interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, data []byte) error
	Receive(ctx context.Context) ([]byte, error)
}