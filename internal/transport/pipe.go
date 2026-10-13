package transport

import (
	"context"
	"errors"
)

var ErrPipeReset = errors.New("pipe reset")

type Pipe interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	SendMessage(ctx context.Context, data []byte, key string) error
	ReceiveMessage(ctx context.Context) ([]byte, error)
	DeleteMessage(ctx context.Context, key string) error

	SessionID() string
	MaxMessageSize() int
}