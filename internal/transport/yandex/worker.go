package yandex

import (
	"context"
	"sync"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

type WorkerConfig struct {
	Threshold    time.Duration
	ScanInterval time.Duration
	BatchSize    int
	BatchPause   time.Duration
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		Threshold:    30 * time.Second,
		ScanInterval: 5 * time.Second,
		BatchSize:    200,
		BatchPause:   50 * time.Millisecond,
	}
}

type CleanupWorker struct {
	creds  *Credentials
	logger logging.ContextLogger
	cfg    WorkerConfig

	client *BoardClient

	mu   sync.Mutex
	seen map[string]time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewCleanupWorker(creds *Credentials, logger logging.ContextLogger, cfg WorkerConfig) *CleanupWorker {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 30 * time.Second
	}
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = 5 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 200
	}
	return &CleanupWorker{
		creds:  creds,
		logger: logger,
		cfg:    cfg,
		seen:   make(map[string]time.Time),
	}
}

func (w *CleanupWorker) Start(ctx context.Context) error {
	boardCfg := BoardConfig{QueueSize: 65536}
	client := NewBoardClient(w.logger, w.creds, boardCfg)
	if err := client.Connect(ctx); err != nil {
		return err
	}
	w.client = client

	initial := client.InitialObjects()
	staleTime := time.Now().Add(-w.cfg.Threshold - time.Second)
	for _, obj := range initial {
		id := extractObjectID(obj)
		if id == "" {
			continue
		}
		w.seen[id] = staleTime
	}

	w.ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(2)
	go w.acceptLoop()
	go w.scanLoop()

	w.logger.Info("yandex-worker: started; initial=", len(initial),
		" orphaned; threshold=", w.cfg.Threshold,
		" scan=", w.cfg.ScanInterval)
	return nil
}

func (w *CleanupWorker) Stop(ctx context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	if w.client != nil {
		_ = w.client.Close()
	}
	w.logger.Info("yandex-worker: stopped")
	return nil
}

func (w *CleanupWorker) acceptLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return
		case event, ok := <-w.client.EventQueue():
			if !ok {
				return
			}

			if _, isRestored := event.(*BoardConnectionRestored); isRestored {
				continue
			}

			action, objects := parseWorkerEvent(event)
			if action == "" {
				continue
			}

			now := time.Now()
			w.mu.Lock()
			for _, obj := range objects {
				id := extractObjectID(obj)
				if id == "" {
					continue
				}
				switch action {
				case "server-modify-objects":
					if _, exists := w.seen[id]; !exists {
						w.seen[id] = now
					}
				case "server-drop-objects":
					delete(w.seen, id)
				}
			}
			w.mu.Unlock()
		}
	}
}

func (w *CleanupWorker) scanLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.cfg.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.scanOnce()
		}
	}
}

func (w *CleanupWorker) scanOnce() {
	now := time.Now()

	w.mu.Lock()
	var stale []string
	for id, t := range w.seen {
		if now.Sub(t) >= w.cfg.Threshold {
			stale = append(stale, id)
		}
	}
	w.mu.Unlock()

	if len(stale) == 0 {
		return
	}

	w.logger.Info("yandex-worker: cleaning ", len(stale), " stale objects")

	for i := 0; i < len(stale); i += w.cfg.BatchSize {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		end := i + w.cfg.BatchSize
		if end > len(stale) {
			end = len(stale)
		}
		batch := stale[i:end]

		objects := make([]map[string]any, 0, len(batch))
		for _, id := range batch {
			objects = append(objects, createBoardObject(
				id, "",
				w.creds.Participant,
				w.creds.Dashboard,
			))
		}

		if err := w.client.DropObjects(w.ctx, objects); err != nil {
			w.logger.Warn("yandex-worker: drop batch failed: ", err)
			continue
		}

		w.mu.Lock()
		for _, id := range batch {
			delete(w.seen, id)
		}
		w.mu.Unlock()

		w.logger.Info("yandex-worker: dropped ", len(batch),
			" (", end, "/", len(stale), ")")

		select {
		case <-w.ctx.Done():
			return
		case <-time.After(w.cfg.BatchPause):
		}
	}
}

func parseWorkerEvent(event any) (string, []any) {
	arr, ok := event.([]any)
	if !ok || len(arr) < 2 {
		return "", nil
	}
	name, _ := arr[0].(string)
	if name != "dashboard" {
		return "", nil
	}
	eventData, ok := arr[1].(map[string]any)
	if !ok {
		return "", nil
	}
	action, _ := eventData["action"].(string)
	if action != "server-modify-objects" && action != "server-drop-objects" {
		return "", nil
	}
	data, ok := eventData["data"].(map[string]any)
	if !ok {
		return "", nil
	}
	objects, ok := data["objects"].([]any)
	if !ok {
		return "", nil
	}
	return action, objects
}

func extractObjectID(obj any) string {
	m, ok := obj.(map[string]any)
	if !ok {
		return ""
	}
	if h, ok := m["hash"].(string); ok && h != "" {
		return h
	}
	attrs, ok := m["_attributes_"].(map[string]any)
	if !ok {
		return ""
	}
	if i, ok := attrs["id"].(string); ok && i != "" {
		return i
	}
	return ""
}