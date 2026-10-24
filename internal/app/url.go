package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	URLScheme      = "shampoore"
	advancedPrefix = "a."
)

func ParseSource(src string) (*Settings, error) {
	if strings.HasPrefix(src, URLScheme+"://") {
		return ParseURL(src)
	}
	return Load(src)
}

func ParseURL(raw string) (*Settings, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("app: parse url: %w", err)
	}
	if u.Scheme != URLScheme {
		return nil, fmt.Errorf("app: unexpected scheme %q", u.Scheme)
	}

	q := u.Query()

	kind := q.Get("type")
	if kind == "" {
		kind = u.Host
	}
	if kind == "" {
		return nil, fmt.Errorf("app: shampoore url missing type")
	}

	d, ok := DescriptorFor(kind)
	if !ok {
		return nil, fmt.Errorf("app: unknown transport type %q", kind)
	}
	if d.DecodeURL == nil {
		return nil, fmt.Errorf("app: transport %q does not support URL parsing", kind)
	}

	s := defaultSettings()
	if v := q.Get("pw"); v != "" {
		s.EncryptionPassword = v
	}
	if u.Fragment != "" {
		s.Name = u.Fragment
	}

	// Юзерские overrides: a.<key>=<value>. Всё декодируется как string —
	// Values.Int/Bool/StringSlice сами сожмут обратно.
	for key, vals := range q {
		if !strings.HasPrefix(key, advancedPrefix) || len(vals) == 0 {
			continue
		}
		realKey := strings.TrimPrefix(key, advancedPrefix)
		if realKey == "" {
			continue
		}
		s.Advanced[realKey] = vals[0]
	}

	data, err := d.DecodeURL(q)
	if err != nil {
		return nil, fmt.Errorf("app: decode transport %q: %w", kind, err)
	}
	s.SetTransportData(kind, data)

	return s, nil
}

func EncodeURL(s *Settings) (string, error) {
	kind := s.Type
	if kind == "" {
		return "", fmt.Errorf("app: transport type not set")
	}

	d, ok := DescriptorFor(kind)
	if !ok {
		return "", fmt.Errorf("app: unknown transport type %q", kind)
	}
	if d.EncodeURL == nil {
		return "", fmt.Errorf("app: transport %q does not support URL encoding", kind)
	}

	q := url.Values{}
	q.Set("type", kind)
	if s.EncryptionPassword != "" {
		q.Set("pw", s.EncryptionPassword)
	}

	if err := d.EncodeURL(s.Transport, q); err != nil {
		return "", fmt.Errorf("app: encode transport %q: %w", kind, err)
	}

	for k, v := range s.Advanced {
		if k == "" || v == nil {
			continue
		}
		q.Set(advancedPrefix+k, encodeValue(v))
	}

	out := URLScheme + "://?" + q.Encode()
	if s.Name != "" {
		out += "#" + url.PathEscape(s.Name)
	}
	return out, nil
}

func encodeValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []string:
		return strings.Join(x, ",")
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, encodeValue(item))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}