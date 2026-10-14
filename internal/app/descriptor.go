package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"

	"github.com/Esteviredzu/shampoore-core/internal/logging"
)

type Builder func(
	ctx context.Context,
	s *Settings,
	role string,
	logger logging.ContextLogger,
	cfg Values,
) (*BuildResult, error)

type Descriptor struct {
	Kind        string
	DisplayName string
	Description string

	DecodeURL func(q url.Values) (json.RawMessage, error)
	EncodeURL func(raw json.RawMessage, q url.Values) error

	Defaults Values

	Build Builder
}

var (
	descriptorsMu sync.RWMutex
	descriptors   = map[string]Descriptor{}
)

func RegisterDescriptor(d Descriptor) error {
	if d.Kind == "" {
		return errors.New("app: descriptor kind is empty")
	}
	if d.Build == nil {
		return fmt.Errorf("app: descriptor %q: Build is nil", d.Kind)
	}
	if d.Defaults == nil {
		d.Defaults = NewValues()
	}

	descriptorsMu.Lock()
	defer descriptorsMu.Unlock()

	if _, exists := descriptors[d.Kind]; exists {
		return fmt.Errorf("app: descriptor %q already registered", d.Kind)
	}
	descriptors[d.Kind] = d
	return nil
}

func DescriptorFor(kind string) (Descriptor, bool) {
	descriptorsMu.RLock()
	defer descriptorsMu.RUnlock()
	d, ok := descriptors[kind]
	return d, ok
}

func AllDescriptors() []Descriptor {
	descriptorsMu.RLock()
	defer descriptorsMu.RUnlock()

	out := make([]Descriptor, 0, len(descriptors))
	for _, d := range descriptors {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Kind < out[j].Kind
	})
	return out
}