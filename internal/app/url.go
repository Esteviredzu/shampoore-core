package app

import (
	"fmt"
	"net/url"
	"strings"
)

const URLScheme = "shampoore"

func ParseSource(src string) (*Settings, error) {
	if strings.HasPrefix(src, URLScheme+"://") {
		return ParseURL(src)
	}
	return Load(src)
}

func ParseURL(raw string) (*Settings, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("app: parse url: %w", err)
	}
	if u.Scheme != URLScheme {
		return nil, fmt.Errorf("app: unexpected scheme %q", u.Scheme)
	}

	kind := u.Host
	if kind == "" {
		return nil, fmt.Errorf("app: shampoore url missing transport kind")
	}

	d, ok := DescriptorFor(kind)
	if !ok {
		return nil, fmt.Errorf("app: unknown transport kind %q", kind)
	}
	if d.DecodeURL == nil {
		return nil, fmt.Errorf("app: transport %q does not support URL parsing", kind)
	}

	q := u.Query()
	s := defaultSettings()

	if v := q.Get("pw"); v != "" {
		s.EncryptionPassword = v
	}
	if u.Fragment != "" {
		s.Name = u.Fragment
	}

	data, err := d.DecodeURL(q)
	if err != nil {
		return nil, fmt.Errorf("app: decode transport %q: %w", kind, err)
	}
	s.SetTransportData(kind, data)

	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func EncodeURL(s *Settings) (string, error) {
	kind := s.TransportKind()
	if kind == "" {
		return "", fmt.Errorf("app: transport kind not set")
	}

	d, ok := DescriptorFor(kind)
	if !ok {
		return "", fmt.Errorf("app: unknown transport kind %q", kind)
	}
	if d.EncodeURL == nil {
		return "", fmt.Errorf("app: transport %q does not support URL encoding", kind)
	}

	q := url.Values{}
	if s.EncryptionPassword != "" {
		q.Set("pw", s.EncryptionPassword)
	}

	if err := d.EncodeURL(s.TransportData(), q); err != nil {
		return "", fmt.Errorf("app: encode transport %q: %w", kind, err)
	}

	u := &url.URL{
		Scheme:   URLScheme,
		Host:     kind,
		RawQuery: q.Encode(),
	}
	if s.Name != "" {
		u.Fragment = s.Name
	}
	return u.String(), nil
}