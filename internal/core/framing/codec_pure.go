//go:build purego || !linux

package framing

import (
	"errors"
	"fmt"

	"codeberg.org/rape4me/kc/vp8"
)

type pureEncoder struct {
	enc *vp8.Encoder
}

func NewEncoder(width, height, fps int, bitrate string) (VideoEncoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("framing/pure: bad dimensions")
	}
	enc, err := vp8.NewEncoder(width, height)
	if err != nil {
		return nil, fmt.Errorf("framing/pure: new encoder: %w", err)
	}
	return &pureEncoder{enc: enc}, nil
}

func (e *pureEncoder) Encode(yuv []byte) ([]byte, error) {
	want := e.enc.Width() * e.enc.Height() * 3 / 2
	if len(yuv) != want {
		return nil, fmt.Errorf("framing/pure: bad yuv size %d, want %d", len(yuv), want)
	}
	frame, err := e.enc.Encode(yuv)
	if err != nil {
		return nil, fmt.Errorf("framing/pure: encode: %w", err)
	}
	return frame, nil
}

func (e *pureEncoder) Close() error {
	if e.enc != nil {
		return e.enc.Close()
	}
	return nil
}

type pureDecoder struct {
	dec *vp8.Decoder
}

func NewDecoder(width, height, fps int) (VideoDecoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("framing/pure: bad dimensions")
	}
	dec, err := vp8.NewDecoder(width, height)
	if err != nil {
		return nil, fmt.Errorf("framing/pure: new decoder: %w", err)
	}
	return &pureDecoder{dec: dec}, nil
}

func (d *pureDecoder) Decode(vp8Frame []byte) ([]byte, error) {
	yuv, err := d.dec.Decode(vp8Frame)
	if err != nil {
		return nil, fmt.Errorf("framing/pure: decode: %w", err)
	}
	return yuv, nil
}

func (d *pureDecoder) Close() error {
	if d.dec != nil {
		return d.dec.Close()
	}
	return nil
}