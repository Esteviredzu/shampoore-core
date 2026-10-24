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

	Type      string          `json:"type,omitempty"`
	Transport json.RawMessage `json:"transport,omitempty"`

	EncryptionPassword string `json:"encryption_password,omitempty"`

	Advanced Values `json:"advanced,omitempty"`

	IPv4Only bool `json:"-"`
}

func (s *Settings) TransportKind() string          { return s.Type }
func (s *Settings) TransportData() json.RawMessage { return s.Transport }

func (s *Settings) SetTransportData(kind string, data json.RawMessage) {
	s.Type = kind
	s.Transport = data
}

func (s *Settings) EffectiveAdvanced() Values {
	return defaultAdvanced().Merge(s.Advanced)
}

func (s *Settings) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	for _, old := range []string{"mode", "socks5", "tun", "platform"} {
		if _, ok := raw[old]; ok {
			return fmt.Errorf(
				"app: field %q is no longer supported in config; "+
					"use \"advanced\" (e.g. tun_mtu, socks5_listen) "+
					"and CLI flag -m for mode", old)
		}
	}

	type plain Settings
	tmp := plain(*s)

	if v, ok := raw["name"]; ok {
		_ = json.Unmarshal(v, &tmp.Name)
	}
	if v, ok := raw["encryption_password"]; ok {
		_ = json.Unmarshal(v, &tmp.EncryptionPassword)
	}
	if v, ok := raw["advanced"]; ok {
		var adv Values
		if err := json.Unmarshal(v, &adv); err == nil {
			tmp.Advanced = adv
		}
	}

	if v, ok := raw["type"]; ok {
		_ = json.Unmarshal(v, &tmp.Type)
	}
	if v, ok := raw["transport"]; ok {
		tmp.Transport = v
	}

	if tmp.Type == "" {
		if v, ok := raw["transport_kind"]; ok {
			var k string
			if err := json.Unmarshal(v, &k); err == nil && k != "" {
				tmp.Type = k
			}
		}
	}
	if tmp.Type != "" && tmp.Transport == nil {
		if v, ok := raw[tmp.Type]; ok {
			tmp.Transport = v
		}
	}

	*s = Settings(tmp)
	return nil
}

func (s *Settings) ApplyDefaults() error {
	if s.Type == "" {
		s.Type = "yandex"
	}
	if s.Advanced == nil {
		s.Advanced = NewValues()
	}
	return nil
}

func Load(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	s := &Settings{IPv4Only: IPv4Only}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if err := s.ApplyDefaults(); err != nil {
		return nil, err
	}
	return s, nil
}