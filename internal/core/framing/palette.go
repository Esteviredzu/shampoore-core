package framing

import (
	"fmt"
	"sync"
)

var BitsPerCell int

var PaletteY []byte

var IdleBGY byte

var lumaToPalette [256]byte

var (
	paletteMu     sync.Mutex
	paletteReady  bool
	paletteFrozen bool
)

func InitPalette(bits int) error {
	if bits < 1 || bits > 8 {
		return fmt.Errorf(
			"framing: bits per cell must be 1..8, got %d", bits)
	}
	paletteMu.Lock()
	defer paletteMu.Unlock()
	if paletteFrozen {
		if BitsPerCell != bits {
			return fmt.Errorf(
				"framing: palette frozen at bits=%d, cannot change to %d",
				BitsPerCell, bits)
		}
		return nil
	}
	if paletteReady && BitsPerCell != bits {
		return fmt.Errorf(
			"framing: palette already initialized with bits=%d, "+
				"cannot change to %d", BitsPerCell, bits)
	}
	rebuildPalette(bits)
	paletteReady = true
	return nil
}

func freezePalette() {
	paletteMu.Lock()
	paletteFrozen = true
	paletteMu.Unlock()
}

func requirePalette(where string) error {
	paletteMu.Lock()
	ready := paletteReady
	paletteMu.Unlock()
	if !ready {
		return fmt.Errorf(
			"framing: %s: palette not initialized "+
				"(call framing.InitPalette first)", where)
	}
	return nil
}

func rebuildPalette(bits int) {
	BitsPerCell = bits
	n := 1 << bits
	step := 256 / n
	PaletteY = make([]byte, n)
	for i := 0; i < n; i++ {
		PaletteY[i] = byte(step/2 + i*step)
	}
	IdleBGY = PaletteY[0]

	for l := 0; l < 256; l++ {
		best := 0
		bestDist := 1 << 30
		for i, pl := range PaletteY {
			d := l - int(pl)
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist = d
				best = i
			}
		}
		lumaToPalette[l] = byte(best)
	}
}