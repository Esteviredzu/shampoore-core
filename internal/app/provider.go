package app

import (
	"context"

	"github.com/Esteviredzu/shampoore-core/internal/core/channel/vp8"
)

type Provider interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Tunnel() *vp8.Tunnel
}