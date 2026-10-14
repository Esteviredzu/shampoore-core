package telemost

import (
	"strings"

	"github.com/google/uuid"
)

func newUUID() string {
	return uuid.NewString()
}

func randomUUIDHex() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}