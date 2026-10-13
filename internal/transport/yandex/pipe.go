package yandex

import (
	"context"
	"encoding/base64"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

const (
	maxValueSize  = 15000
	statsInterval = 5 * time.Second
)

type PipeConfig struct {
	QueueSize     int
	SendBatch     int
	SendFlushMs   int
	DeleteEnabled bool
	DeleteBatch   int
	DeleteFlushMs int
	StatsEnabled  bool
}

func DefaultPipeConfig() PipeConfig {
	return PipeConfig{
		QueueSize:     65536,
		SendBatch:     32,
		SendFlushMs:   100,
		DeleteEnabled: true,
		DeleteBatch:   512,
		DeleteFlushMs: 2000,
		StatsEnabled:  true,
	}
}

type YandexPipe struct {
	client  *BoardClient
	logger  logging.ContextLogger
	cfg     PipeConfig
	session string

	messages   chan []byte
	receiveErr error
	resetCh    chan struct{}

	outboundMu     sync.Mutex
	outboundBuf    map[string]string
	outboundOrder  []string
	outboundTicker *time.Ticker

	deleteMu     sync.Mutex
	deleteBuf    []string
	flushCloseMu sync.Once
	flushDone    chan struct{}

	ctx     context.Context
	cancel  context.CancelFunc
	started bool

	statsMu      sync.Mutex
	sentMessages uint64
	sentBytes    uint64
	recvMessages uint64
	recvBytes    uint64
	statsTicker  *time.Ticker
}

func NewYandexPipe(client *BoardClient, logger logging.ContextLogger, cfg PipeConfig) *YandexPipe {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 65536
	}
	return &YandexPipe{
		client:         client,
		logger:         logger,
		cfg:            cfg,
		session:        uuid.NewString(),
		messages:       make(chan []byte, cfg.QueueSize),
		resetCh:        make(chan struct{}, 1),
		flushDone:      make(chan struct{}),
		outboundBuf:    make(map[string]string),
		outboundTicker: time.NewTicker(time.Duration(cfg.SendFlushMs) * time.Millisecond),
		statsTicker:    time.NewTicker(statsInterval),
	}
}

func (p *YandexPipe) Start(ctx context.Context) error {
	if p.started {
		return nil
	}
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.started = true

	go p.receiveLoop()
	go p.outboundFlushLoop()
	go p.deleteFlushLoop()
	go p.statsLoop()
	return nil
}

func (p *YandexPipe) Stop(ctx context.Context) error {
	if !p.started {
		return nil
	}
	p.started = false

	p.cancel()
	p.flushCloseMu.Do(func() { close(p.flushDone) })

	p.outboundMu.Lock()
	if len(p.outboundBuf) > 0 {
		_ = p.flushOutboundLocked(context.Background())
	}
	p.outboundMu.Unlock()

	p.deleteMu.Lock()
	if len(p.deleteBuf) > 0 {
		_ = p.flushDeletesLocked(context.Background())
	}
	p.deleteMu.Unlock()

	return nil
}

func (p *YandexPipe) SessionID() string   { return p.session }
func (p *YandexPipe) MaxMessageSize() int { return maxValueSize }

func (p *YandexPipe) SendMessage(ctx context.Context, data []byte, key string) error {
	value := base64.StdEncoding.EncodeToString(data)
	objectID := objectIDFromKey(key)

	p.outboundMu.Lock()
	if _, exists := p.outboundBuf[objectID]; !exists {
		p.outboundOrder = append(p.outboundOrder, objectID)
	}
	p.outboundBuf[objectID] = value
	p.outboundMu.Unlock()

	p.statsMu.Lock()
	p.sentMessages++
	p.sentBytes += uint64(len(data))
	p.statsMu.Unlock()
	return nil
}

func (p *YandexPipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.resetCh:
		return nil, transport.ErrPipeReset
	case data, ok := <-p.messages:
		if !ok {
			if p.receiveErr != nil {
				return nil, p.receiveErr
			}
			return nil, io.EOF
		}
		return data, nil
	}
}

func (p *YandexPipe) DeleteMessage(ctx context.Context, key string) error {
	if !p.cfg.DeleteEnabled {
		return nil
	}
	objectID := objectIDFromKey(key)

	p.deleteMu.Lock()
	p.deleteBuf = append(p.deleteBuf, objectID)
	p.deleteMu.Unlock()
	return nil
}

func (p *YandexPipe) outboundFlushLoop() {
	defer p.outboundTicker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.flushDone:
			return
		case <-p.outboundTicker.C:
			p.outboundMu.Lock()
			if len(p.outboundBuf) > 0 {
				_ = p.flushOutboundLocked(context.Background())
			}
			p.outboundMu.Unlock()
		}
	}
}

func (p *YandexPipe) flushOutboundLocked(ctx context.Context) error {
	if len(p.outboundBuf) == 0 {
		return nil
	}

	objects := make([]map[string]any, 0, len(p.outboundBuf))
	valueChanges := make(map[string]bool, len(p.outboundBuf))
	for _, id := range p.outboundOrder {
		value, ok := p.outboundBuf[id]
		if !ok {
			continue
		}
		objects = append(objects, createBoardObject(
			id, value,
			p.client.creds.Participant,
			p.client.creds.Dashboard,
		))
		valueChanges[id] = false
	}

	p.outboundBuf = make(map[string]string)
	p.outboundOrder = nil

	n := len(objects)
	if _, err := p.client.SendObjects(ctx, objects, valueChanges, false); err != nil {
		p.logger.Warn("yandex-pipe: outbound flush failed (",
			n, " objects): ", err)
		return err
	}
	return nil
}

func (p *YandexPipe) receiveLoop() {
	defer close(p.messages)

	for {
		select {
		case <-p.ctx.Done():
			p.receiveErr = io.EOF
			return
		case event, ok := <-p.client.EventQueue():
			if !ok {
				p.receiveErr = io.EOF
				return
			}

			if _, isRestored := event.(*BoardConnectionRestored); isRestored {
				p.logger.Info("yandex-pipe: websocket reconnected, sending reset")
				select {
				case p.resetCh <- struct{}{}:
				default:
				}
				continue
			}

			for _, value := range p.extractValues(event) {
				p.statsMu.Lock()
				p.recvMessages++
				p.recvBytes += uint64(len(value))
				p.statsMu.Unlock()

				select {
				case p.messages <- value:
				case <-p.ctx.Done():
					p.receiveErr = io.EOF
					return
				}
			}
		}
	}
}

func (p *YandexPipe) deleteFlushLoop() {
	ticker := time.NewTicker(time.Duration(p.cfg.DeleteFlushMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.flushDone:
			return
		case <-ticker.C:
			p.deleteMu.Lock()
			if len(p.deleteBuf) > 0 {
				_ = p.flushDeletesLocked(context.Background())
			}
			p.deleteMu.Unlock()
		}
	}
}

func (p *YandexPipe) statsLoop() {
	defer p.statsTicker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.statsTicker.C:
			p.statsMu.Lock()
			s := p.sentMessages
			sb := p.sentBytes
			r := p.recvMessages
			rb := p.recvBytes
			p.sentMessages = 0
			p.sentBytes = 0
			p.recvMessages = 0
			p.recvBytes = 0
			p.statsMu.Unlock()

			if p.cfg.StatsEnabled {
				p.logger.Info("[pipe-stats] sent ", s, " msgs / ", sb,
					" bytes; recv ", r, " msgs / ", rb,
					" bytes (", int(statsInterval.Seconds()), "s)")
			}
		}
	}
}

func (p *YandexPipe) flushDeletesLocked(ctx context.Context) error {
	if len(p.deleteBuf) == 0 {
		return nil
	}
	ids := p.deleteBuf
	p.deleteBuf = nil

	objects := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		objects = append(objects, createBoardObject(
			id, "",
			p.client.creds.Participant,
			p.client.creds.Dashboard,
		))
	}

	if err := p.client.DropObjects(ctx, objects); err != nil {
		p.logger.Warn("yandex-pipe: drop batch failed (", len(ids), " objects): ", err)
		return err
	}
	return nil
}

func (p *YandexPipe) extractValues(event any) [][]byte {
	arr, ok := event.([]any)
	if !ok || len(arr) < 2 {
		return nil
	}
	if name, _ := arr[0].(string); name != "dashboard" {
		return nil
	}
	eventData, ok := arr[1].(map[string]any)
	if !ok {
		return nil
	}
	if action, _ := eventData["action"].(string); action != "server-modify-objects" {
		return nil
	}
	data, ok := eventData["data"].(map[string]any)
	if !ok {
		return nil
	}
	objects, ok := data["objects"].([]any)
	if !ok {
		return nil
	}

	var result [][]byte
	for _, obj := range objects {
		m, ok := obj.(map[string]any)
		if !ok {
			continue
		}
		attrs, ok := m["_attributes_"].(map[string]any)
		if !ok {
			continue
		}

		if creator, _ := attrs["creatorHash"].(string); creator == p.client.creds.Participant {
			continue
		}

		value, ok := attrs["value"].(string)
		if !ok || value == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			continue
		}
		result = append(result, decoded)
	}
	return result
}

func objectIDFromKey(key string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("yandex-pipe:"+key)).String()
}

func createBoardObject(objectID, value, participant, dashboard string) map[string]any {
	return map[string]any{
		"_attributes_": map[string]any{
			"id":    objectID,
			"value": value,
			"style": "html=1;aspect=fixed;fillColor=#00000000;" +
				"strokeColor=#00000000;connectable=0;rotatable=0;" +
				"allowArrows=0;editable=0",
			"vertex":      "1",
			"type":        "dragline",
			"path":        "m 0 0 l 1 1",
			"stroke":      "rgba(17,17,17)",
			"strokeWidth": "1",
			"base_width":  "1",
			"base_height": "1",
			"creatorHash": participant,
			"parent":      dashboard,
			"index":       "1",
		},
		"mxGeometry": []any{
			map[string]any{
				"_attributes_": map[string]any{
					"x": "0", "y": "0",
					"width": "1", "height": "1",
					"as": "geometry",
				},
			},
		},
		"Object": []any{
			map[string]any{
				"_attributes_": map[string]any{
					"x": "-25", "y": "-25", "as": "offset",
				},
			},
		},
		"hash": objectID,
	}
}