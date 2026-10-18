package squares

// EncodeFrame рисует payload в rgb-буфер.
// cellSize — размер клетки на экране (в пикселях).
func EncodeFrame(dst []byte, width, height, cols, rows, cellSize int, payload []byte) {
	fillRect(dst, width, 0, 0, width, height, IdleBG)

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx*4/8 >= len(payload) {
				break
			}
			val := get4Bit(payload, idx*4)
			col := Palette[val]
			fillRect(dst, width, c*cellSize, r*cellSize, cellSize, cellSize, col)
		}
	}
}

// DecodeFrame сэмплирует центры клеток из rgb-буфера.
func DecodeFrame(rgb []byte, width, height, cols, rows, cellSize, payloadBytes int) []byte {
	if len(rgb) < width*height*3 {
		return nil
	}
	payload := make([]byte, payloadBytes)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cx := c*cellSize + cellSize/2
			cy := r*cellSize + cellSize/2
			off := (cy*width + cx) * 3
			if off+2 >= len(rgb) {
				continue
			}
			idx := quantizeGray(rgb[off], rgb[off+1], rgb[off+2])
			cellIdx := r*cols + c
			bitPos := cellIdx * 4
			bytePos := bitPos >> 3
			bitOff := uint(bitPos & 7)
			if bytePos >= len(payload) {
				continue
			}
			payload[bytePos] |= byte(idx << bitOff)
			if bitOff > 4 && bytePos+1 < len(payload) {
				payload[bytePos+1] |= byte(uint(idx) >> (8 - bitOff))
			}
		}
	}
	return payload
}

func get4Bit(payload []byte, bitPos int) byte {
	bytePos := bitPos >> 3
	bitOff := uint(bitPos & 7)
	var v uint16
	if bytePos < len(payload) {
		v |= uint16(payload[bytePos])
	}
	if bytePos+1 < len(payload) {
		v |= uint16(payload[bytePos+1]) << 8
	}
	return byte((uint32(v) >> bitOff) & 0x0F)
}

func lumaOf(r, g, b byte) int {
	return (int(r)*77 + int(g)*150 + int(b)*29) >> 8
}

func quantizeGray(r, g, b byte) byte {
	l := lumaOf(r, g, b)
	idx := (l + 4) >> 4
	if idx > 15 {
		idx = 15
	}
	return byte(idx)
}

func fillRect(dst []byte, width, x0, y0, w, h int, col [3]byte) {
	for y := y0; y < y0+h; y++ {
		rowOff := y * width * 3
		for x := x0; x < x0+w; x++ {
			off := rowOff + x*3
			dst[off] = col[0]
			dst[off+1] = col[1]
			dst[off+2] = col[2]
		}
	}
}