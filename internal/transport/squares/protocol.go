package squares

import "fmt"

// Формат payload (первые HdrLen байт):
//   [0:4]          length   — длина валидных данных (LittleEndian uint32)
//   [4:8]          crc32    — crc32(IEEE) от payload[HdrLen : HdrLen+length]
//   [HdrLen:]      batch    — последовательность записей
//                              каждая: uint16(длина) + данные
//
// Заголовок занимает первые HdrCells 4-битных клеток.
const (
	HdrLen   = 8          // 4 (length) + 4 (crc)
	RecHdr   = 2          // префикс длины каждого сообщения в batch
	HdrCells = HdrLen * 2 // 8 байт = 16 нибблов = 16 клеток
)

// Grid — единственный источник истины для cols/rows/cellSize.
// Из него выводятся width, height, payloadBytes и максимальные размеры
// сообщений. Нигде больше эти формулы не повторяются.
type Grid struct {
	CellSize int
	Cols     int
	Rows     int
}

// DeriveLayout вычисляет производные размеры.
//
// width/height округляются вверх до кратности 16 (требование VP8).
// Если cols*cellSize уже кратно 16 — совпадают с активной зоной,
// иначе кадр шире активной зоны, а хвост заполняется IdleBG.
func (g Grid) DeriveLayout() (
	width, height, payloadBytes, maxData, maxMessage int,
	err error,
) {
	if g.CellSize < 4 || g.CellSize > 32 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"squares: invalid cell_size %d (want 4..32)", g.CellSize)
	}
	if g.Cols <= 0 || g.Rows <= 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"squares: cols/rows must be positive (got %dx%d)", g.Cols, g.Rows)
	}
	cells := g.Cols * g.Rows
	if cells%2 != 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"squares: cells=%d must be even (cols×rows)", cells)
	}

	payloadBytes = cells / 2
	maxData = payloadBytes - HdrLen
	maxMessage = maxData - RecHdr
	if maxMessage < 32 {
		return 0, 0, 0, 0, 0, fmt.Errorf(
			"squares: payload too small (%d bytes, need >= 32)", maxMessage)
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