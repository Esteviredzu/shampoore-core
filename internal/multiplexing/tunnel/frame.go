package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

var frameMagic = [4]byte{'T', 'U', 'N', '1'}

const (
	frameVersion byte = 1

	frameTypeOpen  byte = 1
	frameTypeData  byte = 2
	frameTypeClose byte = 3

	FrameHeaderSize = 26
)

type FrameType byte

const (
	FrameOpen  FrameType = FrameType(frameTypeOpen)
	FrameData  FrameType = FrameType(frameTypeData)
	FrameClose FrameType = FrameType(frameTypeClose)
)

type TunnelFrame struct {
	StreamID string
	Type     FrameType
	Data     []byte
	Metadata map[string]any
}

func EncodeFrame(f *TunnelFrame) ([]byte, error) {
	var payload []byte

	switch f.Type {
	case FrameOpen:
		if len(f.Data) > 0 {
			return nil, fmt.Errorf("tunnel: open frame cannot contain data")
		}
		meta := f.Metadata
		if meta == nil {
			meta = map[string]any{}
		}
		b, err := json.Marshal(meta)
		if err != nil {
			return nil, fmt.Errorf("tunnel: marshal open metadata: %w", err)
		}
		payload = b

	case FrameData:
		payload = f.Data

	case FrameClose:
		if len(f.Data) > 0 {
			return nil, fmt.Errorf("tunnel: close frame cannot contain data")
		}
		if len(f.Metadata) > 0 {
			return nil, fmt.Errorf("tunnel: close frame cannot contain metadata")
		}
		payload = nil

	default:
		return nil, fmt.Errorf("tunnel: unknown frame type: %d", byte(f.Type))
	}

	streamUUID, err := uuid.Parse(f.StreamID)
	if err != nil {
		return nil, fmt.Errorf("tunnel: invalid stream id: %w", err)
	}

	out := make([]byte, FrameHeaderSize+len(payload))
	copy(out[0:4], frameMagic[:])
	out[4] = frameVersion
	out[5] = byte(f.Type)
	copy(out[6:22], streamUUID[:])
	binary.BigEndian.PutUint32(out[22:26], uint32(len(payload)))
	copy(out[26:], payload)
	return out, nil
}

type FrameDecoder struct {
	buf []byte
}

func NewFrameDecoder() *FrameDecoder {
	return &FrameDecoder{buf: make([]byte, 0, 4096)}
}

func (d *FrameDecoder) Reset() {
	d.buf = d.buf[:0]
}

func (d *FrameDecoder) Feed(data []byte) ([]*TunnelFrame, error) {
	d.buf = append(d.buf, data...)

	var frames []*TunnelFrame

	for {
		if len(d.buf) < FrameHeaderSize {
			break
		}

		if d.buf[0] != 'T' || d.buf[1] != 'U' ||
			d.buf[2] != 'N' || d.buf[3] != '1' {
			return frames, fmt.Errorf("tunnel: bad magic: %x", d.buf[0:4])
		}
		if d.buf[4] != frameVersion {
			return frames, fmt.Errorf("tunnel: unsupported version: %d", d.buf[4])
		}

		ftype := d.buf[5]
		var streamUUID uuid.UUID
		copy(streamUUID[:], d.buf[6:22])
		payloadSize := binary.BigEndian.Uint32(d.buf[22:26])

		total := FrameHeaderSize + int(payloadSize)
		if len(d.buf) < total {
			break
		}

		payload := make([]byte, payloadSize)
		copy(payload, d.buf[FrameHeaderSize:total])

		frame := &TunnelFrame{
			StreamID: streamUUID.String(),
			Type:     FrameType(ftype),
		}

		switch FrameType(ftype) {
		case FrameOpen:
			var meta map[string]any
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &meta); err != nil {
					return frames, fmt.Errorf("tunnel: decode open metadata: %w", err)
				}
			}
			if meta == nil {
				meta = map[string]any{}
			}
			frame.Metadata = meta

		case FrameData:
			frame.Data = payload

		case FrameClose:
			if len(payload) > 0 {
				return frames, fmt.Errorf("tunnel: close frame has payload")
			}

		default:
			return frames, fmt.Errorf("tunnel: unknown frame type: %d", ftype)
		}

		frames = append(frames, frame)

		remaining := copy(d.buf, d.buf[total:])
		d.buf = d.buf[:remaining]
	}

	return frames, nil
}