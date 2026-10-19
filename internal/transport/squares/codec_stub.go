//go:build !linux

package squares

import "errors"

var ErrCodecUnavailable = errors.New(
	"squares: VP8 codec not implemented for this platform " +
		"(currently only linux + system ffmpeg is supported)")

func NewEncoder(width, height, fps int, bitrate string) (VideoEncoder, error) {
	return nil, ErrCodecUnavailable
}

func NewDecoder(width, height, fps int) (VideoDecoder, error) {
	return nil, ErrCodecUnavailable
}