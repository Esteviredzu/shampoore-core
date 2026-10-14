package vp8

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	StreamID = "shampoore-vp8"
	TrackID  = "video"
	frameDur = 33 * time.Millisecond

	keepaliveInterval = 200 * time.Millisecond

	vp8HeaderSize = 10

	vp8MaxPayload = (1 << 19) - 1 - 7
)

// keepaliveSample — padded keepalive, того же размера что data (~1000 байт).
// Без постоянного трафика SFU не публикует видеотрек другим участникам.
var keepaliveSample = makeKeepalive()

func makeKeepalive() []byte {
	payload := make([]byte, 990)
	fp := len(payload)

	out := make([]byte, vp8HeaderSize+len(payload))
	out[0] = 0x10 | byte((fp&0x07)<<5)
	out[1] = byte((fp >> 3) & 0xFF)
	out[2] = byte((fp >> 11) & 0xFF)
	out[3] = 0x9d
	out[4] = 0x01
	out[5] = 0x2a
	out[6] = 0x02
	out[7] = 0x00
	out[8] = 0x02
	out[9] = 0x00
	return out
}

type Tunnel struct {
	pubTrack *webrtc.TrackLocalStaticSample
	subTrack *webrtc.TrackRemote

	incoming     chan []byte
	errors       chan error
	subReady     chan struct{}
	subReadyOnce sync.Once

	writeMu     sync.Mutex
	lastWriteAt atomic.Int64

	keepaliveCancel context.CancelFunc

	closeOnce sync.Once
	closed    chan struct{}
}

func NewTunnel(pubPC, subPC *webrtc.PeerConnection) (*Tunnel, error) {
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		TrackID, StreamID,
	)
	if err != nil {
		return nil, err
	}
	if _, err := pubPC.AddTrack(track); err != nil {
		return nil, err
	}
	return AttachTunnel(pubPC, subPC, track)
}

func AttachTunnel(pubPC, subPC *webrtc.PeerConnection, track *webrtc.TrackLocalStaticSample) (*Tunnel, error) {
	t := &Tunnel{
		pubTrack: track,
		incoming: make(chan []byte, 256),
		errors:   make(chan error, 1),
		subReady: make(chan struct{}),
		closed:   make(chan struct{}),
	}

	if subPC != nil {
		subPC.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			mime := remote.Codec().MimeType
			log.Printf("[vp8] OnTrack: %s", mime)

			if mime != webrtc.MimeTypeVP8 {
				go drainTrack(remote)
				return
			}

			t.subTrack = remote
			t.subReadyOnce.Do(func() { close(t.subReady) })
			t.readLoop(remote)
		})
	}

	kaCtx, kaCancel := context.WithCancel(context.Background())
	t.keepaliveCancel = kaCancel
	go t.keepaliveLoop(kaCtx)

	return t, nil
}

func drainTrack(remote *webrtc.TrackRemote) {
	for {
		if _, _, err := remote.ReadRTP(); err != nil {
			return
		}
	}
}

func (t *Tunnel) WriteSample(data []byte) error {
	select {
	case <-t.closed:
		return errors.New("vp8: tunnel closed")
	default:
	}

	if len(data) > vp8MaxPayload {
		return errors.New("vp8: payload too large")
	}

	wrapped := wrapVP8Keyframe(data)

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	err := t.pubTrack.WriteSample(media.Sample{Data: wrapped, Duration: frameDur})
	if err == nil {
		t.lastWriteAt.Store(time.Now().UnixNano())
	}
	return err
}

func (t *Tunnel) keepaliveLoop(ctx context.Context) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.closed:
			return
		case <-ticker.C:
			select {
			case <-t.closed:
				return
			default:
			}

			last := t.lastWriteAt.Load()
			if last != 0 && time.Since(time.Unix(0, last)) < keepaliveInterval {
				continue
			}

			t.writeMu.Lock()
			_ = t.pubTrack.WriteSample(media.Sample{
				Data:     keepaliveSample,
				Duration: frameDur,
			})
			t.lastWriteAt.Store(time.Now().UnixNano())
			t.writeMu.Unlock()
		}
	}
}

func (t *Tunnel) Receive(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, errors.New("vp8: tunnel closed")
	case err := <-t.errors:
		return nil, err
	case data := <-t.incoming:
		return data, nil
	}
}

func (t *Tunnel) WaitSubTrack(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.subReady:
		return nil
	case <-t.closed:
		return errors.New("vp8: tunnel closed")
	}
}

func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		if t.keepaliveCancel != nil {
			t.keepaliveCancel()
		}
		close(t.closed)
	})
	return nil
}

func (t *Tunnel) reportError(err error) {
	select {
	case t.errors <- err:
	default:
	}
}

func wrapVP8Keyframe(data []byte) []byte {
	fp := len(data)

	out := make([]byte, vp8HeaderSize+len(data))

	out[0] = 0x10 | byte((fp&0x07)<<5)
	out[1] = byte((fp >> 3) & 0xFF)
	out[2] = byte((fp >> 11) & 0xFF)

	out[3] = 0x9d
	out[4] = 0x01
	out[5] = 0x2a

	out[6] = 0x02
	out[7] = 0x00
	out[8] = 0x02
	out[9] = 0x00

	copy(out[vp8HeaderSize:], data)
	return out
}

func unwrapVP8Keyframe(data []byte) []byte {
	if len(data) < vp8HeaderSize {
		return nil
	}
	if data[3] != 0x9d || data[4] != 0x01 || data[5] != 0x2a {
		return nil
	}

	fp := int(data[0]>>5&0x07) |
		int(data[1])<<3 |
		int(data[2])<<11

	if fp != len(data)-vp8HeaderSize {
		return nil
	}
	if data[6] != 0x02 || data[7] != 0x00 || data[8] != 0x02 || data[9] != 0x00 {
		return nil
	}

	payload := make([]byte, len(data)-vp8HeaderSize)
	copy(payload, data[vp8HeaderSize:])
	return payload
}

func (t *Tunnel) readLoop(remote *webrtc.TrackRemote) {
	var vp8 codecs.VP8Packet
	var frame []byte
	var expectSeq uint16
	var haveSeq bool
	var valid bool
	var totalFrames int

	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		if haveSeq && pkt.SequenceNumber != expectSeq {
			valid = false
			frame = frame[:0]
		}
		expectSeq = pkt.SequenceNumber + 1
		haveSeq = true

		payload, err := vp8.Unmarshal(pkt.Payload)
		if err != nil {
			valid = false
			frame = frame[:0]
			continue
		}
		if vp8.S == 1 {
			frame = frame[:0]
			valid = true
		}
		if !valid {
			continue
		}
		frame = append(frame, payload...)
		if !pkt.Marker {
			continue
		}

		full := make([]byte, len(frame))
		copy(full, frame)
		frame = frame[:0]
		valid = false

		totalFrames++
		if totalFrames <= 20 || totalFrames%200 == 0 ||
			(len(full) != 1000 && len(full) != 1010) {
			log.Printf("[vp8] recv frame #%d size=%d", totalFrames, len(full))
		}

		unwrapped := unwrapVP8Keyframe(full)
		if unwrapped == nil {
			continue
		}

		select {
		case t.incoming <- unwrapped:
		default:
		}
	}
}

var _ = io.EOF