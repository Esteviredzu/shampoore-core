package yandex

import (
	"encoding/json"
	"errors"
	"fmt"
)

type Config struct {
	Hash            string `json:"hash"`
	ParticipantName string `json:"participant_name,omitempty"`

	PipeQueue         int   `json:"pipe_queue,omitempty"`
	BoardQueue        int   `json:"board_queue,omitempty"`
	SendBatch         int   `json:"send_batch,omitempty"`
	SendFlushMs       int   `json:"send_flush_ms,omitempty"`
	DeleteEnabled     *bool `json:"delete_enabled,omitempty"`
	DeleteBatch       int   `json:"delete_batch,omitempty"`
	DeleteFlushMs     int   `json:"delete_flush_ms,omitempty"`
	StatsDisabled     bool  `json:"stats_disabled,omitempty"`
	HeartbeatDisabled bool  `json:"heartbeat_disabled,omitempty"`

	WorkerEnabled      *bool `json:"worker_enabled,omitempty"`
	WorkerThresholdSec int   `json:"worker_threshold_sec,omitempty"`
	WorkerScanSec      int   `json:"worker_scan_sec,omitempty"`
	WorkerBatch        int   `json:"worker_batch,omitempty"`
	WorkerBatchPauseMs int   `json:"worker_batch_pause_ms,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		PipeQueue:          65536,
		BoardQueue:         65536,
		SendBatch:          32,
		SendFlushMs:        100,
		DeleteBatch:        512,
		DeleteFlushMs:      2000,
		WorkerThresholdSec: 30,
		WorkerScanSec:      5,
		WorkerBatch:        200,
		WorkerBatchPauseMs: 50,
	}
}

func (c *Config) UnmarshalJSON(data []byte) error {
	type alias Config
	tmp := alias(DefaultConfig())
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*c = Config(tmp)
	return nil
}

func (c *Config) Validate() error {
	if c.Hash == "" {
		return errors.New("yandex: hash is required")
	}
	if !isValidHash(c.Hash) {
		return fmt.Errorf("yandex: hash must be 32 hex chars, got %q", c.Hash)
	}
	return nil
}

func (c *Config) EffectiveDeleteEnabled() bool {
	if c.DeleteEnabled != nil {
		return *c.DeleteEnabled
	}
	return true
}

func (c *Config) EffectiveWorkerEnabled() bool {
	if c.WorkerEnabled != nil {
		return *c.WorkerEnabled
	}
	return true
}

func isValidHash(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}