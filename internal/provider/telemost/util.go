package telemost

import (
	"net"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

func newUUID() string {
	return uuid.NewString()
}

func iceHostFromURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, p := range []string{"stuns:", "stun:", "turns:", "turn:"} {
		if strings.HasPrefix(s, p) {
			s = s[len(p):]
			if i := strings.LastIndexByte(s, '@'); i >= 0 {
				s = s[i+1:]
			}
			if i := strings.IndexAny(s, "?#"); i >= 0 {
				s = s[:i]
			}
			if host, _, err := net.SplitHostPort(s); err == nil {
				return host
			}
			return s
		}
	}
	if u, err := url.Parse(s); err == nil {
		return u.Hostname()
	}
	return ""
}