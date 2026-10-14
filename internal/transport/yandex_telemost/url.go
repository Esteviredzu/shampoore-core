package telemost

import (
	"encoding/json"
	"errors"
	"net/url"

	"github.com/Esteviredzu/shampoore-core/internal/app"
)

type urlCodec struct{}

func (urlCodec) Kind() string { return "telemost" }

func (urlCodec) Encode(s *app.Settings, q url.Values) error {
	cfg := DefaultConfig()
	if data := s.TransportData(); len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
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

func (urlCodec) Decode(q url.Values) (json.RawMessage, error) {
	conf := q.Get("conf")
	if conf == "" {
		return nil, errors.New("telemost: missing conf")
	}

	out := map[string]any{
		"conference_link": conf,
	}
	if name := q.Get("name"); name != "" {
		out["display_name"] = name
	}
	return json.Marshal(out)
}

func init() {
	app.RegisterURLCodec(urlCodec{})
}