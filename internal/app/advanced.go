package app

// Advanced — пользовательские переопределения дефолтов транспорта.
// Пустое/нулевое поле = "использовать дефолт транспорта".
type Advanced struct {
	MuxFlushMs        int   `json:"mux_flush_ms,omitempty"`
	MuxFlushThreshold int   `json:"mux_flush_threshold,omitempty"`

	ReliableWindow int `json:"reliable_window,omitempty"`
	ReliableChunk  int `json:"reliable_chunk,omitempty"`

	PaddingEnabled *bool `json:"padding_enabled,omitempty"`
	PaddingTarget  int   `json:"padding_target_size,omitempty"`

	JitterMinMs int `json:"jitter_min_ms,omitempty"`
	JitterMaxMs int `json:"jitter_max_ms,omitempty"`

	VP8SendDelayMs int `json:"vp8_send_delay_ms,omitempty"`
}

// TransportDefaults — значения по умолчанию от конкретного транспорта.
// Транспорт сам их формирует и отдаёт в Advanced.Effective.
type TransportDefaults struct {
	MuxFlushMs        int
	MuxFlushThreshold int

	ReliableWindow int
	ReliableChunk  int

	PaddingEnabled bool
	PaddingTarget  int

	JitterMinMs int
	JitterMaxMs int

	VP8SendDelayMs int
}

// Effective применяет пользовательские override'ы к дефолтам транспорта.
// Пустое поле в Advanced → берётся дефолт.
func (a Advanced) Effective(d TransportDefaults) TransportDefaults {
	if a.MuxFlushMs > 0 {
		d.MuxFlushMs = a.MuxFlushMs
	}
	if a.MuxFlushThreshold > 0 {
		d.MuxFlushThreshold = a.MuxFlushThreshold
	}
	if a.ReliableWindow > 0 {
		d.ReliableWindow = a.ReliableWindow
	}
	if a.ReliableChunk > 0 {
		d.ReliableChunk = a.ReliableChunk
	}
	if a.PaddingEnabled != nil {
		d.PaddingEnabled = *a.PaddingEnabled
	}
	if a.PaddingTarget > 0 {
		d.PaddingTarget = a.PaddingTarget
	}
	if a.JitterMinMs > 0 {
		d.JitterMinMs = a.JitterMinMs
	}
	if a.JitterMaxMs > 0 {
		d.JitterMaxMs = a.JitterMaxMs
	}
	if a.VP8SendDelayMs > 0 {
		d.VP8SendDelayMs = a.VP8SendDelayMs
	}
	return d
}

// MuxConfig — параметры мультиплексора, которые транспорт передаёт
// в main через Runtime. Пользователь их настраивает через Advanced,
// но итоговое значение считает транспорт.
type MuxConfig struct {
	FlushMs        int
	FlushThreshold int
}