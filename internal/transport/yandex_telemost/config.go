package telemost

import "errors"

type Config struct {
	ConferenceLink string `json:"conference_link"`
	DisplayName    string `json:"display_name,omitempty"`
	AppVersion     string `json:"app_version,omitempty"`
	SDKVersion     string `json:"sdk_version,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		DisplayName: "Гость",
		AppVersion:  "213.7.0",
		SDKVersion:  "5.28.0",
	}
}

func (c *Config) Validate() error {
	if c.ConferenceLink == "" {
		return errors.New("telemost: conference_link is required")
	}
	return nil
}

func (c *Config) EffectiveDisplayName() string {
	if c.DisplayName == "" {
		return "Гость"
	}
	return c.DisplayName
}