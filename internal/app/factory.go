package app

import (
	"context"
	"fmt"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

type Builder func(
	ctx context.Context,
	s *Settings,
	role string,
	logger logging.ContextLogger,
) (*Runtime, error)

var registry = map[string]Builder{}

func RegisterTransport(kind string, b Builder) {
	registry[kind] = b
}

func BuildRuntime(
	ctx context.Context,
	s *Settings,
	role string,
	logger logging.ContextLogger,
) (*Runtime, error) {
	builder, ok := registry[s.TransportKind()]
	if !ok {
		return nil, fmt.Errorf("app: unknown transport %q", s.TransportKind())
	}
	return builder(ctx, s, role, logger)
}