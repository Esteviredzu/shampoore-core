package app

import (
	"encoding/json"
	"fmt"
	"os"
)

type Mode string

const (
	ModeSOCKS5 Mode = "socks5"
	ModeTUN    Mode = "tun"
	ModeBoth   Mode = "both"
)

type Settings struct {
	Name string `json:"name,omitempty"`

	EncryptionPassword string `json:"encryption_password"`

	Mode   Mode         `json:"mode,omitempty"`
	SOCKS5 SOCKS5Config `json:"socks5,omitempty"`
	TUN    TUNConfig    `json:"tun,omitempty"`

	Advanced Advanced `json:"advanced,omitempty"`

	transportKind string
	transportData json.RawMessage
}

type SOCKS5Config struct {
	Listen   string `json:"listen,omitempty"`
	IPv4Only bool   `json:"ipv4_only,omitempty"`
}

type TUNConfig struct {
	Name        string   `json:"name,omitempty"`
	MTU         int      `json:"mtu,omitempty"`
	Inet4       string   `json:"inet4,omitempty"`
	AutoRoute   bool     `json:"auto_route,omitempty"`
	BypassHosts []string `json:"bypass_hosts,omitempty"`
	DNSServers  []string `json:"dns_servers,omitempty"`
}

type Advanced struct {
	MuxFlushMs        int `json:"mux_flush_ms,omitempty"`
	MuxFlushThreshold int `json:"mux_flush_threshold,omitempty"`

	ReliableWindow int `json:"reliable_window,omitempty"`
	ReliableChunk  int `json:"reliable_chunk,omitempty"`

	PaddingEnabled bool `json:"padding_enabled,omitempty"`
	PaddingTarget  int  `json:"padding_target_size,omitempty"`
	JitterMinMs    int  `json:"jitter_min_ms,omitempty"`
	JitterMaxMs    int  `json:"jitter_max_ms,omitempty"`
}

func (s *Settings) TUNEnabled() bool               { return s.Mode == ModeTUN || s.Mode == ModeBoth }
func (s *Settings) SOCKS5Enabled() bool            { return s.Mode == ModeSOCKS5 || s.Mode == ModeBoth }
func (s *Settings) TransportKind() string          { return s.transportKind }
func (s *Settings) TransportData() json.RawMessage { return s.transportData }

func (s *Settings) SetTransportData(kind string, data json.RawMessage) {
	s.transportKind = kind
	s.transportData = data
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	type plain Settings
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*s = Settings(p)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	kind := "yandex"
	if v, ok := raw["transport_kind"]; ok {
		var k string
		if err := json.Unmarshal(v, &k); err != nil {
			return err
		}
		if k != "" {
			kind = k
		}
	}
	s.transportKind = kind

	if v, ok := raw[kind]; ok {
		s.transportData = v
	}
	return nil
}

func (s *Settings) validate() error {
	switch s.Mode {
	case ModeSOCKS5, ModeTUN, ModeBoth:
	case "":
		s.Mode = ModeSOCKS5
	default:
		return fmt.Errorf("app: invalid mode %q", s.Mode)
	}

	if s.transportKind == "" {
		s.transportKind = "yandex"
	}
	return nil
}

func Load(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	s := defaultSettings()
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}