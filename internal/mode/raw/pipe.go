package raw

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/core/channel/vp8"
	"github.com/Esteviredzu/shampoore-core/internal/core/framing"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

const (
	fakeVP8Hdr     = 10
	payloadHdr     = 8
	recHdr         = 2
	sendQueueCap   = 8192
	enqueueTimeout = 50 * time.Millisecond
	maxTunnelFrame = 500_000
	keyframeW      = 128
	keyframeH      = 128
)

var fakePFrameHeader = []byte{
	0x01, 0x00, 0x00, 0x9d, 0x01, 0x2a,
	0x80, 0x02,
	0xe0, 0x01,
}

type Config struct {
	FrameSize int
	FPS       int
	Copies    int
	GOP       int
}

type Pipe struct {
	tunnel *vp8.Tunnel
	logger logging.ContextLogger
	cfg    Config

	frameDur time.Duration
	maxData  int

	sendCh chan []byte
	recvCh chan []byte

	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	stopOnce sync.Once
	wg       sync.WaitGroup

	carry []byte

	keyframe   []byte
	frameCount int

	stFrames     atomic.Int64
	stBytes      atomic.Int64
	stRecvFrames atomic.Int64
	stRecvBytes  atomic.Int64
	stRecvMsgs   atomic.Int64
	stDrops      atomic.Int64
	stKeyframes  atomic.Int64
}

func New(t *vp8.Tunnel, cfg Config, logger logging.ContextLogger) *Pipe {
	if cfg.FrameSize <= 0 {
		cfg.FrameSize = 12_000
	}
	if cfg.FrameSize > maxTunnelFrame {
		cfg.FrameSize = maxTunnelFrame
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 60
	}
	if cfg.Copies <= 0 {
		cfg.Copies = 1
	}
	if cfg.GOP <= 0 {
		cfg.GOP = 30
	}
	maxData := cfg.FrameSize - fakeVP8Hdr - payloadHdr
	if maxData < 64 {
		maxData = 64
	}
	return &Pipe{
		tunnel:   t,
		logger:   logger,
		cfg:      cfg,
		frameDur: time.Second / time.Duration(cfg.FPS),
		maxData:  maxData,
		sendCh:   make(chan []byte, sendQueueCap),
		recvCh:   make(chan []byte, 512),
	}
}

func (p *Pipe) Start(ctx context.Context) error {
	if p.started {
		return nil
	}
	kf, err := generateVP8Keyframe(keyframeW, keyframeH)
	if err != nil {
		return fmt.Errorf("raw: generate keyframe: %w", err)
	}
	if len(kf) == 0 {
		return errors.New("raw: empty keyframe")
	}
	p.keyframe = kf
	p.logger.Info("raw: keyframe ready; size=", len(kf), " gop=", p.cfg.GOP)

	p.ctx, p.cancel = context.WithCancel(ctx)
	p.started = true
	p.wg.Add(2)
	go p.sendLoop()
	go p.recvLoop()
	p.logger.Info("raw: started; frame_size=", p.cfg.FrameSize,
		" maxData=", p.maxData, " fps=", p.cfg.FPS,
		" copies=", p.cfg.Copies, " gop=", p.cfg.GOP)
	return nil
}

func (p *Pipe) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
	p.wg.Wait()
	return nil
}

func (p *Pipe) SessionID() string                                   { return "raw" }
func (p *Pipe) DeleteMessage(ctx context.Context, key string) error { return nil }
func (p *Pipe) MaxMessageSize() int                                 { return p.maxData - recHdr }

func (p *Pipe) SendMessage(ctx context.Context, data []byte, key string) error {
	if !p.started {
		return errors.New("raw: not started")
	}
	if len(data) == 0 {
		return nil
	}
	if len(data)+recHdr > p.maxData {
		return fmt.Errorf("raw: message too large (%d > %d)", len(data), p.maxData-recHdr)
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	t := time.NewTimer(enqueueTimeout)
	defer t.Stop()
	select {
	case p.sendCh <- cp:
		return nil
	case <-t.C:
		p.stDrops.Add(1)
		return errors.New("raw: send queue full")
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

func (p *Pipe) ReceiveMessage(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case data, ok := <-p.recvCh:
		if !ok {
			return nil, io.EOF
		}
		return data, nil
	}
}

func (p *Pipe) sendLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.frameDur)
	defer ticker.Stop()

	var repeat [][]byte
	var repeatLeft int
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}

		p.frameCount++
		isKeyframe := p.frameCount == 1 || (p.frameCount%p.cfg.GOP) == 0

		var err error
		if isKeyframe {
			err = p.emitKeyframe()
		} else {
			var batch [][]byte
			if repeatLeft > 0 {
				batch = repeat
				repeatLeft--
			} else {
				batch = p.takeBatch()
				if len(batch) > 0 && p.cfg.Copies > 1 {
					repeat = batch
					repeatLeft = p.cfg.Copies - 1
				}
			}
			if len(batch) == 0 {
				err = p.sendIdle()
			} else {
				err = p.emitPFrame(batch)
			}
		}
		if err != nil {
			if p.ctx.Err() != nil {
				return
			}
			p.logger.Warn("raw: send error: ", err)
		}
	}
}

func (p *Pipe) takeBatch() [][]byte {
	var out [][]byte
	used := 0
	if p.carry != nil {
		out = append(out, p.carry)
		used += len(p.carry) + recHdr
		p.carry = nil
	}
	for used < p.maxData {
		select {
		case m := <-p.sendCh:
			if used+len(m)+recHdr > p.maxData {
				p.carry = m
				return out
			}
			out = append(out, m)
			used += len(m) + recHdr
		default:
			return out
		}
	}
	return out
}

func (p *Pipe) emitKeyframe() error {
	if err := p.tunnel.WriteRawVP8(p.keyframe); err != nil {
		return err
	}
	n := p.stFrames.Add(1)
	k := p.stKeyframes.Add(1)
	p.logger.Debug("raw: SEND KEYFRAME #", k,
		" vp8=", len(p.keyframe), " total_frames=", n)
	return nil
}

func (p *Pipe) sendIdle() error {
	payload := make([]byte, payloadHdr)
	binary.LittleEndian.PutUint32(payload[0:4], 0)
	binary.LittleEndian.PutUint32(payload[4:8], crc32.ChecksumIEEE(nil))

	frame := make([]byte, fakeVP8Hdr+len(payload))
	copy(frame, fakePFrameHeader)
	copy(frame[fakeVP8Hdr:], payload)

	if err := p.tunnel.WriteRawVP8(frame); err != nil {
		return err
	}
	n := p.stFrames.Add(1)
	if n <= 5 || n%300 == 0 {
		p.logger.Debug("raw: SEND idle #", n, " vp8=", len(frame))
	}
	return nil
}

func (p *Pipe) emitPFrame(batch [][]byte) error {
	data := make([]byte, 0, p.maxData)
	for _, m := range batch {
		n := len(m)
		data = append(data, byte(n), byte(n>>8))
		data = append(data, m...)
	}

	payload := make([]byte, payloadHdr+len(data))
	binary.LittleEndian.PutUint32(payload[0:4], uint32(len(data)))
	binary.LittleEndian.PutUint32(payload[4:8], crc32.ChecksumIEEE(data))
	copy(payload[payloadHdr:], data)

	frame := make([]byte, fakeVP8Hdr+len(payload))
	copy(frame, fakePFrameHeader)
	copy(frame[fakeVP8Hdr:], payload)

	if err := p.tunnel.WriteRawVP8(frame); err != nil {
		return err
	}
	n := p.stFrames.Add(1)
	p.stBytes.Add(int64(len(data)))
	p.logger.Debug("raw: SEND P-frame #", n,
		" msgs=", len(batch), " data=", len(data), " vp8=", len(frame))
	return nil
}

func (p *Pipe) recvLoop() {
	defer p.wg.Done()
	defer close(p.recvCh)

	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		raw, err := p.tunnel.Receive(p.ctx)
		if err != nil {
			if p.ctx.Err() == nil {
				p.logger.Warn("raw: recv exit: ", err)
			}
			return
		}
		if len(raw) < 1 {
			continue
		}

		if (raw[0] & 0x01) == 0 {
			rf := p.stRecvFrames.Add(1)
			if rf <= 5 || rf%300 == 0 {
				p.logger.Debug("raw: RECV KEYFRAME #", rf, " raw=", len(raw))
			}
			continue
		}

		if len(raw) < fakeVP8Hdr+payloadHdr {
			continue
		}
		payload := raw[fakeVP8Hdr:]
		length := binary.LittleEndian.Uint32(payload[0:4])
		if length == 0 {
			continue
		}

		if int(length) > len(payload)-payloadHdr {
			p.logger.Warn("raw: BAD-LEN raw=", len(raw),
				" length=", length, " avail=", len(payload)-payloadHdr)
			continue
		}
		crcGot := binary.LittleEndian.Uint32(payload[4:8])
		data := payload[payloadHdr : payloadHdr+int(length)]
		if crc32.ChecksumIEEE(data) != crcGot {
			n := p.stDrops.Add(1)
			if n <= 5 || n%20 == 0 {
				p.logger.Warn("raw: CRC-FAIL drops=", n,
					" raw=", len(raw), " data=", len(data))
			}
			continue
		}

		off := 0
		n := 0
		for off+recHdr <= len(data) {
			mlen := int(data[off]) | int(data[off+1])<<8
			off += recHdr
			if mlen == 0 {
				break
			}
			if off+mlen > len(data) {
				break
			}
			out := make([]byte, mlen)
			copy(out, data[off:off+mlen])
			off += mlen
			n++
			select {
			case p.recvCh <- out:
			case <-p.ctx.Done():
				return
			}
		}
		rf := p.stRecvFrames.Add(1)
		p.stRecvBytes.Add(int64(len(data)))
		p.stRecvMsgs.Add(int64(n))
		if n > 0 {
			p.logger.Debug("raw: RECV P-frame #", rf,
				" msgs=", n, " data=", len(data), " raw=", len(raw))
		}
	}
}

func generateVP8Keyframe(width, height int) ([]byte, error) {
	enc, err := framing.NewEncoder(width, height, 30, "100K")
	if err != nil {
		return nil, err
	}
	defer enc.Close()

	yuv := make([]byte, width*height*3/2)
	for i := 0; i < width*height; i++ {
		yuv[i] = 0x10
	}
	for i := width * height; i < len(yuv); i++ {
		yuv[i] = 128
	}
	return enc.Encode(yuv)
}

var _ transport.Pipe = (*Pipe)(nil)