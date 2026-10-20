package framing

func YUVSize(w, h int) int { return w * h * 3 / 2 }

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

func EncodeFrameYUV(yuv []byte, width, height, cols, rows, cellSize int, payload []byte) {
	// Заливаем Y-плоскость фоном.
	for i := 0; i < width*height; i++ {
		yuv[i] = IdleBGY
	}
	bits := BitsPerCell
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx*bits/8 >= len(payload) {
				break
			}
			val := getNBits(payload, idx*bits, bits)
			fillYRect(yuv, width, c*cellSize, r*cellSize, cellSize, cellSize, PaletteY[val])
		}
	}
}

func DecodeFrameYUV(yuv []byte, width, cols, rows, cellSize, payloadBytes int) []byte {
	if len(yuv) < width*rows*cellSize {
		return nil
	}
	bits := BitsPerCell
	payload := make([]byte, payloadBytes)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cx := c*cellSize + cellSize/2
			cy := r*cellSize + cellSize/2
			l := yuv[cy*width+cx]
			idx := lumaToPalette[l]
			cellIdx := r*cols + c
			setNBits(payload, cellIdx*bits, bits, idx)
		}
	}
	return payload
}

func fillYRect(dst []byte, width, x0, y0, w, h int, val byte) {
	for y := y0; y < y0+h; y++ {
		rowOff := y * width
		for x := x0; x < x0+w; x++ {
			dst[rowOff+x] = val
		}
	}
}