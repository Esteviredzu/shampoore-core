package app

import (
	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

type BuildResult struct {
	Transport   transport.Transport
	Provider    Provider
	BypassHosts []string
	Side        []SideService
}