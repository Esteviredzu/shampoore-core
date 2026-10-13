package yandex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	boardOrigin   = "https://boards.yandex.ru"
	httpTimeout   = 20 * time.Second
	userAgent     = "Mozilla/5.0 (X11; Linux x86_64; rv:155.0) Gecko/20100101 Firefox/155.0"
	nameMinLength = 5
	nameMaxLength = 10
	nameAlphabet  = "abcdefghijklmnopqrstuvwxyz"
)

type Credentials struct {
	Cookie          string
	Session         string
	Dashboard       string
	Presentation    string
	Participant     string
	UserHash        string
	Intermediate    string
	ParticipantName string
}

func BoardURLFromHash(hash string) string {
	return boardOrigin + "/whiteboard/?hash=" + hash
}

func Bootstrap(ctx context.Context, hash string, name string) (*Credentials, error) {
	if !isValidHash(hash) {
		return nil, fmt.Errorf("invalid board hash: %q", hash)
	}

	chosenName := name
	if chosenName == "" {
		chosenName = generateParticipantName()
	}

	guestURL := boardOrigin + "/guest/?hash=" + hash

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Timeout: httpTimeout,
		Jar:     jar,
	}

	cookieHeader, tokenValue, err := requestGuestToken(
		ctx, client, guestURL, hash, chosenName,
	)
	if err != nil {
		return nil, err
	}

	payload, err := decodeJWTPayload(tokenValue)
	if err != nil {
		return nil, err
	}

	userHash, _ := payload["u"].(string)
	if userHash == "" {
		userHash = uuid.NewString()
	}

	jwtName, _ := payload["n"].(string)
	participantName := jwtName
	if participantName == "" {
		participantName = chosenName
	}

	return &Credentials{
		Cookie:          cookieHeader,
		Session:         uuid.NewString(),
		Dashboard:       hash,
		Presentation:    hash,
		Participant:     uuid.NewString(),
		UserHash:        userHash,
		Intermediate:    uuid.NewString(),
		ParticipantName: participantName,
	}, nil
}

func requestGuestToken(
	ctx context.Context,
	client *http.Client,
	guestURL string,
	presentation string,
	name string,
) (string, string, error) {
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, guestURL, nil)
	if err != nil {
		return "", "", err
	}
	getReq.Header.Set("User-Agent", userAgent)
	getReq.Header.Set("Accept-Language", "ru,en-US;q=0.7,en;q=0.3")

	getResp, err := client.Do(getReq)
	if err != nil {
		return "", "", fmt.Errorf("GET %s: %w", guestURL, err)
	}
	io.Copy(io.Discard, getResp.Body)
	getResp.Body.Close()

	if getResp.StatusCode >= 400 {
		return "", "", fmt.Errorf("GET %s -> %d", guestURL, getResp.StatusCode)
	}

	inner := map[string]string{"name": name, "hash": presentation}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return "", "", err
	}
	contentB64 := base64.StdEncoding.EncodeToString(innerJSON)

	body := map[string]string{
		"action":  "request-guest-token",
		"content": contentB64,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", "", err
	}

	postReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		boardOrigin+"/api",
		strings.NewReader(string(bodyJSON)),
	)
	if err != nil {
		return "", "", err
	}
	postReq.Header.Set("User-Agent", userAgent)
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	postReq.Header.Set("Origin", boardOrigin)
	postReq.Header.Set("Referer", guestURL)
	postReq.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")

	postResp, err := client.Do(postReq)
	if err != nil {
		return "", "", fmt.Errorf("POST %s/api: %w", boardOrigin, err)
	}
	io.Copy(io.Discard, postResp.Body)
	postResp.Body.Close()

	if postResp.StatusCode >= 400 {
		return "", "", fmt.Errorf("POST %s/api -> %d", boardOrigin, postResp.StatusCode)
	}

	tokenValue := findTokenCookie(client.Jar, presentation)
	if tokenValue == "" {
		return "", "", fmt.Errorf("cookie token_%s not found", presentation)
	}

	cookieHeader := buildCookieHeader(client.Jar)
	return cookieHeader, tokenValue, nil
}

func findTokenCookie(jar http.CookieJar, presentation string) string {
	wanted := "token_" + presentation
	u, _ := url.Parse(boardOrigin)
	for _, c := range jar.Cookies(u) {
		if c.Name == wanted {
			return c.Value
		}
	}
	return ""
}

func buildCookieHeader(jar http.CookieJar) string {
	u, _ := url.Parse(boardOrigin)
	cookies := jar.Cookies(u)
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func decodeJWTPayload(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("token is not a JWT")
	}

	padded := parts[1]
	if mod := len(padded) % 4; mod != 0 {
		padded += strings.Repeat("=", 4-mod)
	}

	raw, err := base64.URLEncoding.DecodeString(padded)
	if err != nil {
		return nil, fmt.Errorf("invalid JWT base64: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid JWT JSON: %w", err)
	}
	return payload, nil
}

func generateParticipantName() string {
	length := nameMinLength + rand.IntN(nameMaxLength-nameMinLength+1)
	var sb strings.Builder
	sb.Grow(length)
	for i := 0; i < length; i++ {
		sb.WriteByte(nameAlphabet[rand.IntN(len(nameAlphabet))])
	}
	return sb.String()
}