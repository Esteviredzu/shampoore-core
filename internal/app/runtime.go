package app

import (
	"context"

	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

type SideService interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type Runtime struct {
	Transport    transport.Transport
	Provider     Provider
	BypassHosts  []string
	MuxConfig    MuxConfig
	sideServices []SideService
	started      bool
}

func NewRuntime(
	t transport.Transport,
	provider Provider,
	bypass []string,
	side []SideService,
) *Runtime {
	return &Runtime{
		Transport:    t,
		Provider:     provider,
		BypassHosts:  bypass,
		sideServices: side,
	}
}

func (r *Runtime) Start(ctx context.Context) error {
	if r.started {
		return nil
	}
	for i, svc := range r.sideServices {
		if err := svc.Start(ctx); err != nil {
			for j := i - 1; j >= 0; j-- {
				_ = r.sideServices[j].Stop(ctx)
			}
			return err
		}
	}
	r.started = true
	return nil
}

func (r *Runtime) Stop(ctx context.Context) error {
	if !r.started {
		return nil
	}
	r.started = false
	for i := len(r.sideServices) - 1; i >= 0; i-- {
		_ = r.sideServices[i].Stop(ctx)
	}
	if r.Provider != nil {
		_ = r.Provider.Stop(ctx)
	}
	return nil
}