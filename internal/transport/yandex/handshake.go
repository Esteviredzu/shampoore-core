package yandex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (c *BoardClient) engineHandshake(ctx context.Context) error {
	for {
		raw, err := c.recvRaw(ctx)
		if err != nil {
			return err
		}
		if raw == "" {
			continue
		}
		c.logger.Trace("<<< RAW: ", raw)

		if strings.HasPrefix(raw, EngineOpen) {
			var data map[string]any
			if err := json.Unmarshal([]byte(raw[1:]), &data); err != nil {
				return fmt.Errorf("invalid engine.io handshake: %s: %w", raw, err)
			}
			c.logger.Debug("Engine.IO handshake: ", raw[1:])
			if ping, ok := data["pingInterval"]; ok {
				c.logger.Debug("Engine.IO ping interval: ", ping)
			}
			return nil
		}

		if raw == EnginePing {
			if err := c.sendRaw(ctx, Pong()); err != nil {
				return err
			}
		}
	}
}

func (c *BoardClient) socketIOConnect(ctx context.Context) error {
	c.logger.Debug("SOCKET.IO CONNECT")

	if err := c.sendRaw(ctx, Connect()); err != nil {
		return err
	}

	for {
		raw, err := c.recvRaw(ctx)
		if err != nil {
			return err
		}
		if raw == "" {
			continue
		}
		c.logger.Trace("<<< RAW: ", raw)

		if raw == EnginePing {
			if err := c.sendRaw(ctx, Pong()); err != nil {
				return err
			}
			continue
		}

		if strings.HasPrefix(raw, "40") {
			c.logger.Debug("Socket.IO connected")
			return nil
		}
	}
}

func (c *BoardClient) subscribeIM(ctx context.Context) error {
	c.logger.Debug("IM SUBSCRIBE")

	packet := `420["im",{"operation":"subscribe","user":null}]`
	if err := c.sendRaw(ctx, packet); err != nil {
		return err
	}

	gotSubscribed := false
	gotAck := false

	for !gotSubscribed || !gotAck {
		raw, err := c.recvRaw(ctx)
		if err != nil {
			return err
		}
		if raw == "" {
			continue
		}
		c.logger.Trace("<<< RAW: ", raw)

		if raw == `42["subscribed"]` {
			gotSubscribed = true
			continue
		}
		if strings.HasPrefix(raw, "430") {
			gotAck = true
			continue
		}
		if raw == EnginePing {
			if err := c.sendRaw(ctx, Pong()); err != nil {
				return err
			}
		}
	}

	c.logger.Debug("IM subscription complete")
	return nil
}

func (c *BoardClient) subscribeDashboard(ctx context.Context) error {
	c.logger.Debug("DASHBOARD SUBSCRIBE")

	payload := map[string]any{
		"action": "subscribe-slide-dashboard",
		"data": map[string]any{
			"session":      c.creds.Session,
			"dashboard":    c.creds.Dashboard,
			"presentation": c.creds.Presentation,
			"properties": map[string]any{
				"guest_mode":    true,
				"guest_role":    1,
				"guest_password": nil,
				"current_slide": c.creds.Dashboard,
			},
			"participant":           c.creds.Participant,
			"participant_team_role": -1,
			"options":               c.makeDashboardOptions(),
		},
		"participant": c.creds.Participant,
	}

	packetID := 1
	pkt, err := Event("dashboard", payload, packetID)
	if err != nil {
		return err
	}
	if err := c.sendRaw(ctx, pkt); err != nil {
		return err
	}

	for {
		raw, err := c.recvRaw(ctx)
		if err != nil {
			return err
		}
		if raw == "" {
			continue
		}
		c.logger.Trace("<<< RAW: ", raw)

		if raw == EnginePing {
			if err := c.sendRaw(ctx, Pong()); err != nil {
				return err
			}
			continue
		}

		parsed, err := Parse(raw)
		if err != nil {
			continue
		}

		if parsed.SocketType == SocketEvent {
			continue
		}

		if parsed.SocketType == SocketACK && parsed.HasID && parsed.PacketID == packetID {
			c.logger.Debug("Dashboard subscription complete")

			if arr, ok := parsed.Payload.([]any); ok && len(arr) > 0 {
				if data, ok := arr[0].(map[string]any); ok {
					if objs, ok := data["objects"].([]any); ok {
						c.mu.Lock()
						c.initialObjects = make([]map[string]any, 0, len(objs))
						for _, o := range objs {
							if m, ok := o.(map[string]any); ok {
								c.initialObjects = append(c.initialObjects, m)
							}
						}
						count := len(c.initialObjects)
						c.mu.Unlock()
						c.logger.Debug("initial objects: ", count)
					}
				}
			}
			return nil
		}
	}
}

func (c *BoardClient) getParticipantNumbers(ctx context.Context) error {
	c.logger.Debug("GET PARTICIPANT NUMBERS")

	packetID := 2
	payload := map[string]any{
		"action":      "get-client-participant_numbers",
		"data":        map[string]any{},
		"participant": c.creds.Participant,
	}

	pkt, err := Event("dashboard", payload, packetID)
	if err != nil {
		return err
	}
	if err := c.sendRaw(ctx, pkt); err != nil {
		return err
	}

	for {
		raw, err := c.recvRaw(ctx)
		if err != nil {
			return err
		}
		if raw == "" {
			continue
		}
		c.logger.Trace("<<< RAW: ", raw)

		if raw == EnginePing {
			if err := c.sendRaw(ctx, Pong()); err != nil {
				return err
			}
			continue
		}

		parsed, err := Parse(raw)
		if err != nil {
			continue
		}

		if parsed.SocketType == SocketEvent {
			continue
		}

		if parsed.SocketType == SocketACK && parsed.HasID && parsed.PacketID == packetID {
			if arr, ok := parsed.Payload.([]any); ok && len(arr) > 0 {
				if res, ok := arr[0].(map[string]any); ok {
					if r, ok := res["result"].(float64); ok && r == 1 {
						c.logger.Debug("participant numbers request successful")
						return nil
					}
				}
			}
			return fmt.Errorf("participant numbers request failed: %s", raw)
		}
	}
}

func (c *BoardClient) dashboardIn(ctx context.Context) error {
	payload := map[string]any{
		"action": "in",
		"data":   map[string]any{"p": 2, "s": 0, "n": 0},
	}
	pkt, err := EventNoID("dashboard", payload)
	if err != nil {
		return err
	}
	return c.sendRaw(ctx, pkt)
}

func (c *BoardClient) dashboardHeartbeat(ctx context.Context, packetID int) error {
	payload := map[string]any{
		"action":      "heartbeat",
		"data":        map[string]any{},
		"participant": c.creds.Participant,
	}
	pkt, err := Event("dashboard", payload, packetID)
	if err != nil {
		return err
	}
	return c.sendRaw(ctx, pkt)
}

func (c *BoardClient) makeDashboardOptions() map[string]any {
	return map[string]any{
		"type": "landing",
		"participant": map[string]any{
			"hash":     c.creds.Participant,
			"partner":  "yandex",
			"userHash": c.creds.UserHash,
			"name":     c.creds.ParticipantName,
			"additional": map[string]any{
				"guest": true,
			},
			"module":       "yandex",
			"presentation": c.creds.Presentation,
			"identityCandidates": map[string]any{
				"uidHash":    nil,
				"legacyHash": c.creds.Participant,
				"uid":        nil,
				"partner":    "yandex",
			},
			"module_type":            "yandex",
			"participantCaptionName": c.creds.ParticipantName,
		},
		"intermediate": c.creds.Intermediate,
		"device": map[string]any{
			"screen":         map[string]any{"width": 1920, "height": 1080},
			"browser":        "Firefox",
			"browserVersion": "155",
			"os":             "Linux",
			"cookies":        true,
		},
	}
}