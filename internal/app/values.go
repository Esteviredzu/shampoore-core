package app

import (
	"encoding/json"
	"time"
)

type Values map[string]any

func NewValues() Values {
	return Values{}
}

func (v Values) Clone() Values {
	out := make(Values, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}

func (v Values) Merge(override Values) Values {
	out := v.Clone()
	for k, val := range override {
		out[k] = val
	}
	return out
}

func (v Values) Has(key string) bool {
	_, ok := v[key]
	return ok
}

func (v Values) Int(key string, def int) int {
	raw, ok := v[key]
	if !ok || raw == nil {
		return def
	}
	switch n := raw.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return def
		}
		return int(i)
	default:
		return def
	}
}

func (v Values) Int64(key string, def int64) int64 {
	return int64(v.Int(key, int(def)))
}

func (v Values) Bool(key string, def bool) bool {
	raw, ok := v[key]
	if !ok || raw == nil {
		return def
	}
	b, ok := raw.(bool)
	if !ok {
		return def
	}
	return b
}

func (v Values) String(key string, def string) string {
	raw, ok := v[key]
	if !ok || raw == nil {
		return def
	}
	s, ok := raw.(string)
	if !ok {
		return def
	}
	return s
}

func (v Values) Duration(key string, def time.Duration) time.Duration {
	ms := v.Int(key, int(def/time.Millisecond))
	return time.Duration(ms) * time.Millisecond
}