package telemost

import (
	"encoding/json"
	"errors"
	"net/url"
)

func DecodeURL(q url.Values) (json.RawMessage, error) {
	conf := q.Get("conf")
	if conf == "" {
		return nil, errors.New("telemost: missing conf")
	}
	out := map[string]any{"conference_link": conf}
	if name := q.Get("name"); name != "" {
		out["display_name"] = name
	}
	return json.Marshal(out)
}

func EncodeURL(raw json.RawMessage, q url.Values) error {
	cfg := DefaultConfig()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return err
		}
	}
	if cfg.ConferenceLink != "" {
		q.Set("conf", cfg.ConferenceLink)
	}
	if cfg.DisplayName != "" {
		q.Set("name", cfg.DisplayName)
	}
	return nil
}