package yandex

import (
	"encoding/json"
	"errors"
	"net/url"

	"github.com/Esteviredzu/shampoore-core/internal/app"
)

type urlCodec struct{}

func (urlCodec) Kind() string { return "yandex" }

func (urlCodec) Encode(s *app.Settings, q url.Values) error {
	cfg := DefaultConfig()
	if data := s.TransportData(); len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
	}

	if cfg.Hash != "" {
		q.Set("hash", cfg.Hash)
	}
	return nil
}

func (urlCodec) Decode(q url.Values) (json.RawMessage, error) {
	hash := q.Get("hash")
	if hash == "" {
		return nil, errors.New("yandex: missing hash")
	}
	if !isValidHash(hash) {
		return nil, errors.New("yandex: invalid hash")
	}
	return json.Marshal(map[string]any{"hash": hash})
}

func init() {
	app.RegisterURLCodec(urlCodec{})
}