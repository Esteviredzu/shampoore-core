package yandex

import (
	"encoding/json"
	"errors"
	"net/url"
)

func decodeURL(q url.Values) (json.RawMessage, error) {
	hash := q.Get("hash")
	if hash == "" {
		return nil, errors.New("yandex: missing hash")
	}
	if !isValidHash(hash) {
		return nil, errors.New("yandex: invalid hash")
	}
	return json.Marshal(map[string]any{"hash": hash})
}

func encodeURL(raw json.RawMessage, q url.Values) error {
	cfg := DefaultConfig()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return err
		}
	}
	if cfg.Hash != "" {
		q.Set("hash", cfg.Hash)
	}
	return nil
}