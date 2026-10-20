package framing

func getNBits(payload []byte, bitPos, bits int) byte {
	bytePos := bitPos >> 3
	bitOff := uint(bitPos & 7)

	var v uint32
	if bytePos < len(payload) {
		v |= uint32(payload[bytePos])
	}
	if bytePos+1 < len(payload) {
		v |= uint32(payload[bytePos+1]) << 8
	}
	if bytePos+2 < len(payload) && bitOff+uint(bits) > 16 {
		v |= uint32(payload[bytePos+2]) << 16
	}
	mask := uint32((1 << bits) - 1)
	return byte((v >> bitOff) & mask)
}

func setNBits(payload []byte, bitPos, bits int, val byte) {
	bytePos := bitPos >> 3
	bitOff := uint(bitPos & 7)

	mask := byte((1 << bits) - 1)
	val &= mask

	if bytePos < len(payload) {
		payload[bytePos] |= val << bitOff
	}
	if bitOff+uint(bits) > 8 && bytePos+1 < len(payload) {
		payload[bytePos+1] |= val >> (8 - bitOff)
	}
	if bitOff+uint(bits) > 16 && bytePos+2 < len(payload) {
		payload[bytePos+2] |= val >> (16 - bitOff)
	}
}

func EncodeFrame(dst []byte, width, height, cols, rows, cellSize int, payload []byte) {
	fillRect(dst, width, 0, 0, width, height, IdleBG)
	bits := BitsPerCell

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx*bits/8 >= len(payload) {
				break
			}
			val := getNBits(payload, idx*bits, bits)
			col := Palette[val]
			fillRect(dst, width, c*cellSize, r*cellSize, cellSize, cellSize, col)
		}
	}
}

func DecodeFrame(rgb []byte, width, height, cols, rows, cellSize, payloadBytes int) []byte {
	if len(rgb) < width*height*3 {
		return nil
	}
	bits := BitsPerCell
	payload := make([]byte, payloadBytes)

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cx := c*cellSize + cellSize/2
			cy := r*cellSize + cellSize/2
			off := (cy*width + cx) * 3
			if off+2 >= len(rgb) {
				continue
			}
			l := lumaOf(rgb[off], rgb[off+1], rgb[off+2])
			idx := lumaToPalette[l]
			cellIdx := r*cols + c
			setNBits(payload, cellIdx*bits, bits, idx)
		}
	}
	return payload
}

func lumaOf(r, g, b byte) int {
	return (int(r)*77 + int(g)*150 + int(b)*29) >> 8
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