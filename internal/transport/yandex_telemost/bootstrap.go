package telemost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	apiBase     = "https://cloud-api.yandex.ru"
	pageOrigin  = "https://telemost.yandex.ru"
	userAgent   = "Mozilla/5.0 (X11; Linux x86_64; rv:155.0) Gecko/20100101 Firefox/155.0"
	httpTimeout = 20 * time.Second
)

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type ConferenceInfo struct {
	URI              string
	PeerID           string
	RoomID           string
	SessionID        string
	PeerSessionID    string
	Credentials      string
	MediaServerURL   string
	ServiceName      string
	ICEServers       []ICEServer
	StateCheckPeriod time.Duration
}

type bootstrapResult struct {
	info   *ConferenceInfo
	jar    http.CookieJar
	http   *http.Client
	instID string
}

func bootstrap(ctx context.Context, cfg *Config) (*bootstrapResult, error) {
	if cfg.LocalSignalingURL != "" {
		return fakeBootstrap(cfg.LocalSignalingURL), nil
	}

	conferenceLink := strings.TrimSpace(cfg.ConferenceLink)
	if conferenceLink == "" {
		return nil, errors.New("telemost: empty conference link")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Timeout: httpTimeout,
		Jar:     jar,
	}

	instID := newUUID()

	pageURL := pageOrigin + "/j/" + extractConferenceID(conferenceLink)
	if err := fetchPage(ctx, httpClient, pageURL); err != nil {
		return nil, fmt.Errorf("telemost: get page: %w", err)
	}

	info, err := fetchConnection(ctx, httpClient, instID, cfg, conferenceLink)
	if err != nil {
		return nil, err
	}

	return &bootstrapResult{
		info:   info,
		jar:    jar,
		http:   httpClient,
		instID: instID,
	}, nil
}

// fakeBootstrap возвращает синтетический bootstrapResult для локального
// signaling-сервера. Никаких HTTP-запросов к Yandex не делается.
func fakeBootstrap(url string) *bootstrapResult {
	return &bootstrapResult{
		info: &ConferenceInfo{
			PeerID:           newUUID(),
			RoomID:           newUUID(),
			SessionID:        newUUID(),
			PeerSessionID:    newUUID(),
			Credentials:      "local",
			MediaServerURL:   url,
			ServiceName:      "local",
			ICEServers:       nil,
			StateCheckPeriod: 30 * time.Second,
		},
		instID: newUUID(),
	}
}

func fetchPage(ctx context.Context, c *http.Client, pageURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru,en-US;q=0.7,en;q=0.3")

	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func fetchConnection(
	ctx context.Context,
	c *http.Client,
	instID string,
	cfg *Config,
	conferenceLink string,
) (*ConferenceInfo, error) {
	q := url.Values{}
	q.Set("next_gen_media_platform_allowed", "true")
	q.Set("display_name", cfg.EffectiveDisplayName())
	q.Set("waiting_room_supported", "true")

	path := "/telemost_front/v2/telemost/conferences/" +
		url.QueryEscape(conferenceLink) + "/connection?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+path, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("Referer", pageOrigin+"/")
	req.Header.Set("Origin", pageOrigin)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Client-Instance-Id", instID)
	req.Header.Set("X-Telemost-Client-Version", cfg.AppVersion)
	req.Header.Set("idempotency-key", newUUID())

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telemost: connection request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telemost: connection status %d: %s", resp.StatusCode, string(data))
	}

	var raw struct {
		ConnectionType string `json:"connection_type"`
		URI            string `json:"uri"`
		RoomID         string `json:"room_id"`
		PeerID         string `json:"peer_id"`
		SessionID      string `json:"session_id"`
		PeerSessionID  string `json:"peer_session_id"`
		Credentials    string `json:"credentials"`
		MediaPlatform  string `json:"media_platform"`
		ClientConfig   struct {
			MediaServerURL        string      `json:"media_server_url"`
			ServiceName           string      `json:"service_name"`
			StateCheckIntervalSec int         `json:"state_check_interval_seconds"`
			ICEServers            []ICEServer `json:"ice_servers"`
		} `json:"client_configuration"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("telemost: parse connection: %w", err)
	}

	if raw.PeerID == "" || raw.Credentials == "" || raw.ClientConfig.MediaServerURL == "" {
		return nil, fmt.Errorf("telemost: incomplete connection response: %s", string(data))
	}

	period := time.Duration(raw.ClientConfig.StateCheckIntervalSec) * time.Second
	if period <= 0 {
		period = 30 * time.Second
	}

	return &ConferenceInfo{
		URI:              raw.URI,
		PeerID:           raw.PeerID,
		RoomID:           raw.RoomID,
		SessionID:        raw.SessionID,
		PeerSessionID:    raw.PeerSessionID,
		Credentials:      raw.Credentials,
		MediaServerURL:   raw.ClientConfig.MediaServerURL,
		ServiceName:      raw.ClientConfig.ServiceName,
		ICEServers:       raw.ClientConfig.ICEServers,
		StateCheckPeriod: period,
	}, nil
}

func extractConferenceID(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}