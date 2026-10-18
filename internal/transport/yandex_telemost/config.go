package telemost

import "errors"

type Config struct {
	ConferenceLink string `json:"conference_link"`
	DisplayName    string `json:"display_name,omitempty"`
	AppVersion     string `json:"app_version,omitempty"`
	SDKVersion     string `json:"sdk_version,omitempty"`

	// Role всегда SPEAKER: SFU Телемоста закрывает соединение с 1011
	// при PARTICIPANT + sendVideo=true.
	Role string `json:"role,omitempty"`

	// UseSharing — публиковать видео как screen sharing вместо камеры.
	// Ставится серверу, чтобы SFU форвардил его независимо от роли.
	UseSharing bool `json:"use_sharing,omitempty"`

	LocalSignalingURL string `json:"local_signaling_url,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		DisplayName: "Гость",
		AppVersion:  "213.7.0",
		SDKVersion:  "5.28.0",
		Role:        "SPEAKER",
	}
}

func (c *Config) Validate() error {
	if c.LocalSignalingURL == "" && c.ConferenceLink == "" {
		return errors.New("telemost: conference_link or local_signaling_url is required")
	}
	if c.Role == "" {
		c.Role = "SPEAKER"
	}
	if c.Role != "SPEAKER" {
		return errors.New("telemost: only SPEAKER role is supported")
	}
	return nil
}

func (c *Config) EffectiveDisplayName() string {
	if c.DisplayName == "" {
		return "Гость"
	}
	return c.DisplayName
}