package telemost

import (
	"context"
	"net/url"
)

func Bootstrap(ctx context.Context, cfg *Config) (*bootstrapResult, error) {
	return bootstrap(ctx, cfg)
}

func (p *Pipe) BypassHosts() []string {
	if p.boot == nil {
		return nil
	}
	bypass := []string{
		"telemost.yandex.ru",
		"cloud-api.yandex.ru",
	}
	if u, err := url.Parse(p.boot.info.MediaServerURL); err == nil && u.Hostname() != "" {
		bypass = append(bypass, u.Hostname())
	}
	for _, ice := range p.boot.info.ICEServers {
		for _, raw := range ice.URLs {
			if h := iceHostFromURL(raw); h != "" {
				bypass = append(bypass, h)
			}
		}
	}
	return bypass
}