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

func (s *Settings) TUNEnabled() bool               { return s.Mode == ModeTUN || s.Mode == ModeBoth }
func (s *Settings) SOCKS5Enabled() bool            { return s.Mode == ModeSOCKS5 || s.Mode == ModeBoth }
func (s *Settings) TransportKind() string          { return s.transportKind }
func (s *Settings) TransportData() json.RawMessage { return s.transportData }

func (s *Settings) SetTransportData(kind string, data json.RawMessage) {
	s.transportKind = kind
	s.transportData = data
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	type plain Settings
	tmp := plain(*s)

	if v, ok := raw["name"]; ok {
		_ = json.Unmarshal(v, &tmp.Name)
	}
	if v, ok := raw["encryption_password"]; ok {
		_ = json.Unmarshal(v, &tmp.EncryptionPassword)
	}
	if v, ok := raw["mode"]; ok {
		_ = json.Unmarshal(v, &tmp.Mode)
	}
	if v, ok := raw["socks5"]; ok {
		_ = json.Unmarshal(v, &tmp.SOCKS5)
	}
	if v, ok := raw["tun"]; ok {
		_ = json.Unmarshal(v, &tmp.TUN)
	}
	if v, ok := raw["advanced"]; ok {
		_ = json.Unmarshal(v, &tmp.Advanced)
	}

	*s = Settings(tmp)

	kind := s.transportKind
	if kind == "" {
		kind = "yandex"
	}
	if v, ok := raw["transport_kind"]; ok {
		var k string
		if err := json.Unmarshal(v, &k); err == nil && k != "" {
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

	if s.SOCKS5.Listen == "" {
		s.SOCKS5.Listen = "127.0.0.1:1080"
	}
	if s.TUN.Name == "" {
		s.TUN.Name = "shampoore0"
	}
	if s.TUN.MTU == 0 {
		s.TUN.MTU = 1500
	}
	if s.TUN.Inet4 == "" {
		s.TUN.Inet4 = "172.19.0.1/30"
	}
	if len(s.TUN.DNSServers) == 0 {
		s.TUN.DNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}
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