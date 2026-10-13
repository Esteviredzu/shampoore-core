package yandex

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

const (
	boardsWSPath = "wss://socket63.boards.yandex.ru/socket.io/?EIO=4&transport=websocket"
	boardsOrigin = "https://boards.yandex.ru"
	boardsUA     = "Mozilla/5.0 (X11; Linux x86_64; rv:155.0) Gecko/20100101 Firefox/155.0"

	reconnectInitialDelay = 1 * time.Second
	reconnectMaxDelay     = 30 * time.Second
)

type BoardConfig struct {
	QueueSize         int
	HeartbeatDisabled bool
}

func DefaultBoardConfig() BoardConfig {
	return BoardConfig{QueueSize: 65536}
}

type BoardConnectionRestored struct {
	Generation int
}

type BoardClient struct {
	logger logging.ContextLogger
	creds  *Credentials
	cfg    BoardConfig

	mu         sync.Mutex
	ws         *websocket.Conn
	connected  bool
	closing    bool
	generation int

	eventQueue chan any

	ackMu           sync.Mutex
	ackWaiters      map[int]chan any
	packetIDCounter int

	initialObjects []map[string]any

	readerDone       chan struct{}
	supervisorCancel context.CancelFunc
}

func NewBoardClient(logger logging.ContextLogger, creds *Credentials, cfg BoardConfig) *BoardClient {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 65536
	}
	return &BoardClient{
		logger:          logger,
		creds:           creds,
		cfg:             cfg,
		eventQueue:      make(chan any, cfg.QueueSize),
		ackWaiters:      make(map[int]chan any),
		packetIDCounter: 8,
		readerDone:      make(chan struct{}, 1),
	}
}

func (c *BoardClient) EventQueue() <-chan any { return c.eventQueue }

func (c *BoardClient) InitialObjects() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.initialObjects))
	copy(out, c.initialObjects)
	return out
}

func (c *BoardClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return nil
	}
	c.closing = false
	c.mu.Unlock()

	if err := c.establishConnection(ctx); err != nil {
		return err
	}

	c.mu.Lock()
	c.generation++
	c.connected = true
	c.mu.Unlock()

	go c.readerLoop(ctx)

	supervisorCtx, cancel := context.WithCancel(context.Background())
	c.supervisorCancel = cancel
	go c.supervisor(supervisorCtx)

	return nil
}

func (c *BoardClient) Close() error {
	c.mu.Lock()
	c.closing = true
	c.connected = false
	if c.supervisorCancel != nil {
		c.supervisorCancel()
		c.supervisorCancel = nil
	}
	ws := c.ws
	c.ws = nil
	c.mu.Unlock()

	c.ackMu.Lock()
	for _, ch := range c.ackWaiters {
		close(ch)
	}
	c.ackWaiters = make(map[int]chan any)
	c.ackMu.Unlock()

	if ws != nil {
		_ = ws.Close(websocket.StatusNormalClosure, "closing")
	}
	return nil
}

func (c *BoardClient) establishConnection(ctx context.Context) error {
	c.logger.Debug("connecting to Yandex Boards...")

	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"User-Agent":   []string{boardsUA},
			"Cookie":       []string{c.creds.Cookie},
			"Origin":       []string{boardsOrigin},
			"X-Auth-Proxy": []string{"true"},
		},
	}

	ws, resp, err := websocket.Dial(ctx, boardsWSPath, opts)
	if err != nil {
		if resp != nil {
			return errors.New("websocket dial: status " +
				strconv.Itoa(resp.StatusCode) + ": " + err.Error())
		}
		return err
	}
	ws.SetReadLimit(-1)

	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()

	c.logger.Debug("WebSocket connection established")

	if err := c.engineHandshake(ctx); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "engine.io failed")
		return err
	}
	if err := c.socketIOConnect(ctx); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "socket.io failed")
		return err
	}
	if err := c.subscribeIM(ctx); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "im failed")
		return err
	}
	if err := c.subscribeDashboard(ctx); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "dashboard failed")
		return err
	}
	if err := c.getParticipantNumbers(ctx); err != nil {
		_ = ws.Close(websocket.StatusInternalError, "participants failed")
		return err
	}
	if err := c.dashboardIn(ctx); err != nil {
		return err
	}
	if err := c.dashboardIn(ctx); err != nil {
		return err
	}
	if err := c.dashboardHeartbeat(ctx, 3); err != nil {
		return err
	}

	c.logger.Debug("Board connection established")
	return nil
}

func (c *BoardClient) sendRaw(ctx context.Context, data string) error {
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return errors.New("websocket not connected")
	}
	c.logger.Trace(">>> RAW: ", data)
	return ws.Write(ctx, websocket.MessageText, []byte(data))
}

func (c *BoardClient) recvRaw(ctx context.Context) (string, error) {
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return "", errors.New("websocket not connected")
	}
	_, data, err := ws.Read(ctx)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *BoardClient) readerLoop(ctx context.Context) {
	defer func() {
		select {
		case c.readerDone <- struct{}{}:
		default:
		}
	}()

	for {
		c.mu.Lock()
		if c.closing {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()

		raw, err := c.recvRaw(ctx)
		if err != nil {
			c.mu.Lock()
			closing := c.closing
			c.mu.Unlock()
			if !closing {
				c.logger.Warn("reader loop error: ", err)
			}
			return
		}

		if raw == EnginePing {
			if err := c.sendRaw(ctx, Pong()); err != nil {
				return
			}
			continue
		}

		pkt, err := Parse(raw)
		if err != nil {
			continue
		}

		switch pkt.SocketType {
		case SocketACK:
			if pkt.HasID {
				c.ackMu.Lock()
				ch, ok := c.ackWaiters[pkt.PacketID]
				c.ackMu.Unlock()
				if ok {
					select {
					case ch <- pkt.Payload:
					default:
					}
				}
			}
		case SocketEvent:
			select {
			case c.eventQueue <- pkt.Payload:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (c *BoardClient) supervisor(ctx context.Context) {
	delay := reconnectInitialDelay

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.readerDone:
		}

		c.mu.Lock()
		if c.closing {
			c.mu.Unlock()
			return
		}
		c.connected = false
		if c.ws != nil {
			_ = c.ws.Close(websocket.StatusAbnormalClosure, "reconnecting")
			c.ws = nil
		}
		c.mu.Unlock()

		c.ackMu.Lock()
		for _, ch := range c.ackWaiters {
			close(ch)
		}
		c.ackWaiters = make(map[int]chan any)
		c.ackMu.Unlock()

		c.logger.Warn("connection lost; reconnecting...")

		delay = reconnectInitialDelay
		for {
			c.logger.Info("reconnecting in ", delay)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}

			if err := c.establishConnection(ctx); err != nil {
				c.logger.Warn("reconnect failed: ", err)
				delay *= 2
				if delay > reconnectMaxDelay {
					delay = reconnectMaxDelay
				}
				continue
			}

			c.mu.Lock()
			c.generation++
			c.connected = true
			gen := c.generation
			c.mu.Unlock()

			select {
			case c.eventQueue <- &BoardConnectionRestored{Generation: gen}:
			default:
			}

			c.logger.Info("connection restored (generation ", gen, ")")

			go c.readerLoop(ctx)
			break
		}
	}
}

func (c *BoardClient) nextPacketID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.packetIDCounter
	c.packetIDCounter++
	return id
}

func (c *BoardClient) SendDashboardEvent(ctx context.Context, payload map[string]any, waitAck bool) (any, error) {
	packetID := c.nextPacketID()

	pkt, err := Event("dashboard", payload, packetID)
	if err != nil {
		return nil, err
	}

	var ackCh chan any
	if waitAck {
		ackCh = make(chan any, 1)
		c.ackMu.Lock()
		c.ackWaiters[packetID] = ackCh
		c.ackMu.Unlock()
		defer func() {
			c.ackMu.Lock()
			delete(c.ackWaiters, packetID)
			c.ackMu.Unlock()
		}()
	}

	if err := c.sendRaw(ctx, pkt); err != nil {
		return nil, err
	}

	if !waitAck {
		return nil, nil
	}

	select {
	case ack, ok := <-ackCh:
		if !ok {
			return nil, errors.New("connection closed while waiting for ACK")
		}
		return ack, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *BoardClient) SendObjects(ctx context.Context, objects []map[string]any, valueChanges map[string]bool, waitAck bool) (any, error) {
	payload := map[string]any{
		"action": "modify-objects",
		"data": map[string]any{
			"objects":      objects,
			"valueChanges": valueChanges,
		},
		"participant": c.creds.Participant,
	}

	result, err := c.SendDashboardEvent(ctx, payload, waitAck)
	if err != nil {
		return nil, err
	}

	if waitAck {
		if arr, ok := result.([]any); ok && len(arr) > 0 {
			if first, ok := arr[0].(map[string]any); ok {
				if r, ok := first["result"].(bool); ok && r {
					return result, nil
				}
			}
		}
		return nil, errors.New("modify-objects failed")
	}

	return nil, nil
}

func (c *BoardClient) DropObjects(ctx context.Context, objects []map[string]any) error {
	if len(objects) == 0 {
		return nil
	}

	payload := map[string]any{
		"action":      "drop-objects",
		"data":        map[string]any{"objects": objects},
		"participant": c.creds.Participant,
	}

	_, err := c.SendDashboardEvent(ctx, payload, false)
	return err
}

func (c *BoardClient) SendHeartbeat(ctx context.Context, packetID int) error {
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

func (c *BoardClient) StartPeriodicHeartbeat(ctx context.Context) {
	if c.cfg.HeartbeatDisabled {
		c.logger.Info("yandex-board: periodic heartbeat disabled")
		return
	}
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				packetID := c.nextPacketID()
				if err := c.SendHeartbeat(ctx, packetID); err != nil {
					c.logger.Warn("heartbeat failed: ", err)
				}
			}
		}
	}()
}