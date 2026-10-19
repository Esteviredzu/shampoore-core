package framing

import "fmt"

const (
	HdrLen   = 8
	RecHdr   = 2
	HdrCells = HdrLen * 2
)

type Grid struct {
	CellSize int
	Cols     int
	Rows     int
}

func (g Grid) DeriveLayout() (
	width, height, payloadBytes, maxData, maxMessage int,
	err error,
) {
	if g.CellSize < 4 || g.CellSize > 32 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"framing: invalid cell_size %d (want 4..32)", g.CellSize)
	}
	if g.Cols <= 0 || g.Rows <= 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"framing: cols/rows must be positive (got %dx%d)", g.Cols, g.Rows)
	}
	cells := g.Cols * g.Rows
	if cells%2 != 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"framing: cells=%d must be even (cols×rows)", cells)
	}

	payloadBytes = cells / 2
	maxData = payloadBytes - HdrLen
	maxMessage = maxData - RecHdr
	if maxMessage < 32 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"framing: payload too small (%d bytes, need >= 32)", maxMessage)
	}

	width = roundUp16(g.Cols * g.CellSize)
	height = roundUp16(g.Rows * g.CellSize)
	return
}

func roundUp16(v int) int {
	if v%16 == 0 {
		return v
	}
	return ((v / 16) + 1) * 16
}