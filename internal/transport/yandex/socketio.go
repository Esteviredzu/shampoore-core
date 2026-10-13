package yandex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	EngineOpen    = "0"
	EngineClose   = "1"
	EnginePing    = "2"
	EnginePong    = "3"
	EngineMessage = "4"
)

const (
	SocketConnect    = "0"
	SocketDisconnect = "1"
	SocketEvent      = "2"
	SocketACK        = "3"
	SocketError      = "4"
)

type Packet struct {
	EngineType string
	SocketType string
	PacketID   int
	HasID      bool
	Payload    any
}

func Event(eventName string, data any, packetID int) (string, error) {
	payload := []any{eventName, data}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal event payload: %w", err)
	}
	var sb strings.Builder
	sb.Grow(len(EngineMessage) + 1 + 4 + len(raw))
	sb.WriteString(EngineMessage)
	sb.WriteString(SocketEvent)
	sb.WriteString(strconv.Itoa(packetID))
	sb.Write(raw)
	return sb.String(), nil
}

func EventNoID(eventName string, data any) (string, error) {
	payload := []any{eventName, data}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal event payload: %w", err)
	}
	return EngineMessage + SocketEvent + string(raw), nil
}

func Connect() string { return EngineMessage + SocketConnect }
func Pong() string    { return EnginePong }

func Parse(raw string) (*Packet, error) {
	if raw == "" {
		return nil, errors.New("parse: empty packet")
	}

	engineType := raw[:1]

	if engineType != EngineMessage {
		return &Packet{
			EngineType: engineType,
			Payload:    raw[1:],
		}, nil
	}

	socketData := raw[1:]
	if socketData == "" {
		return &Packet{EngineType: engineType}, nil
	}

	socketType := socketData[:1]
	rest := socketData[1:]

	packetID := 0
	hasID := false
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		packetID = packetID*10 + int(rest[i]-'0')
		hasID = true
		i++
	}
	rest = rest[i:]

	var payload any
	if rest != "" {
		if err := json.Unmarshal([]byte(rest), &payload); err != nil {
			payload = rest
		}
	}

	return &Packet{
		EngineType: engineType,
		SocketType: socketType,
		PacketID:   packetID,
		HasID:      hasID,
		Payload:    payload,
	}, nil
}