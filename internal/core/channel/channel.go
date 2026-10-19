package channel

import "context"

type MediaChannel interface {
	WriteRawVP8(data []byte) error
	Receive(ctx context.Context) ([]byte, error)
	Close() error
}