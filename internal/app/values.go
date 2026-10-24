package app

import (
	"encoding/json"
	"strconv"
	"strings"
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
	case string:
		// Приходит из URL-декода (всё строкой).
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return def
		}
		return i
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
	switch b := raw.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
		return def
	default:
		return def
	}
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

func (v Values) StringSlice(key string, def []string) []string {
	raw, ok := v[key]
	if !ok || raw == nil {
		return def
	}
	switch s := raw.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	case string:
		if s == "" {
			return def
		}
		parts := strings.Split(s, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	default:
		return def
	}
}