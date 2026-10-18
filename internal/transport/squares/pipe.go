// FILE: internal/transport/squares/pipe.go
package squares

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/transport"
	"github.com/Esteviredzu/shampoore-core/internal/transport/vp8"
)

const (
	hdrLen         = 8
	recHdr         = 2
	hdrCells       = 16
	sendQueueCap   = 1024
	enqueueTimeout = 500 * time.Millisecond
	cellSize       = 16
)

type Config struct {
	Width, Height int
	Cols, Rows    int
	FPS           int
	Bitrate       string
	Copies        int
}

func DefaultConfig() Config {
	return Config{
		Width:   640,
		Height:  360,
		Cols:    40,
		Rows:    20,
		FPS:     25,
		Bitrate: "600K",
		Copies:  1,
	}
}

type Pipe struct {
	tunnel *vp8.Tunnel
	cfg    Config
	enc    *Encoder
	dec    *Decoder

	cells        int
	payloadBytes int
	maxMessage   int
	maxData      int

	frameDur time.Duration

	sendCh chan []byte
	carry  []byte
	recvCh chan []byte

	ctx    context.Context
	cancel context.CancelFunc

	started  bool
	stopOnce sync.Once
	wg       sync.WaitGroup

	idleSeq uint32
	sharing bool

	rgbBuf []byte

	stFrames atomic.Int64
	stBytes  atomic.Int64
	stMsgs   atomic.Int64
	stFull   atomic.Int64
}

func NewPipe(tunnel *vp8.Tunnel, cfg Config) (*Pipe, error) {
	if tunnel == nil {
		return nil, errors.New("squares: nil tunnel")
	}
	if cfg.Width <= 0 {
		cfg.Width = 384
	}
	if cfg.Height <= 0 {
		cfg.Height = 216
	}
	if cfg.Cols <= 0 {
		cfg.Cols = 24
	}
	if cfg.Rows <= 0 {
		cfg.Rows = 9
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 30
	}
	if cfg.Bitrate == "" {
		cfg.Bitrate = "400K"
	}
	if cfg.Copies <= 0 {
		cfg.Copies = 1
	}

	activeW := cfg.Cols * cellSize
	activeH := cfg.Rows * cellSize
	if activeW > cfg.Width {
		return nil, fmt.Errorf("squares: active area %dpx exceeds width %dpx",
			activeW, cfg.Width)
	}
	if activeH > cfg.Height {
		return nil, fmt.Errorf("squares: active area %dpx exceeds height %dpx",
			activeH, cfg.Height)
	}

	cells := cfg.Cols * cfg.Rows
	if cells%2 != 0 {
		return nil, fmt.Errorf("squares: cells=%d must be even", cells)
	}
	payloadBytes := cells / 2

	maxData := payloadBytes - hdrLen
	maxMessage := maxData - recHdr

	if maxMessage < 32 {
		return nil, fmt.Errorf("squares: payload too small (%d bytes)", maxMessage)
	}
	return &Pipe{
		tunnel:       tunnel,
		cfg:          cfg,
		cells:        cells,
		payloadBytes: payloadBytes,
		maxMessage:   maxMessage,
		maxData:      maxData,
		frameDur:     time.Second / time.Duration(cfg.FPS),
		sendCh:       make(chan []byte, sendQueueCap),
		recvCh:       make(chan []byte, 512),
	}, nil
}

func (p *Pipe) Start(ctx context.Context) error {
	if p.started {
		return nil
	}
	enc, err := NewEncoder(p.cfg.Width, p.cfg.Height, p.cfg.FPS, p.cfg.Bitrate)
	if err != nil {
		return err
	}
	dec, err := NewDecoder(p.cfg.Width, p.cfg.Height, p.cfg.FPS)
	if err != nil {
		_ = enc.Close()
		return err
	}
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.enc = enc
	p.dec = dec
	p.rgbBuf = make([]byte, p.cfg.Width*p.cfg.Height*3)
	p.started = true
	p.wg.Add(2)
	go p.recvLoop()
	go p.sendLoop()
	return nil
}

func (p *Pipe) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
	p.wg.Wait()
	if p.enc != nil {
		_ = p.enc.Close()
	}
	if p.dec != nil {
		_ = p.dec.Close()
	}
	return nil
}

func (p *Pipe) SessionID() string                                   { return "squares" }
func (p *Pipe) MaxMessageSize() int                                 { return p.maxMessage }
func (p *Pipe) DeleteMessage(ctx context.Context, key string) error { return nil }
func (p *Pipe) SetSharing(on bool)                                  { p.sharing = on }

func (p *Pipe) SendMessage(ctx context.Context, data []byte, key string) error {
	if !p.started {
		return errors.New("squares: pipe not started")
	}
	if len(data) == 0 {
		return nil
	}
	if len(data) > p.maxMessage {
		return fmt.Errorf("squares: message too large (%d > %d)",
			len(data), p.maxMessage)
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	t := time.NewTimer(enqueueTimeout)
	defer t.Stop()
	select {
	case p.sendCh <- cp:
		return nil
	case <-t.C:
		n := p.stFull.Add(1)
		if n <= 5 || n%100 == 0 {
			log.Printf("[squares] send queue FULL (%d bytes pending), "+
				"dropping %d bytes — KCP will retransmit (total drops=%d)",
				len(p.sendCh), len(cp), n)
		}
		return errors.New("squares: send queue full")
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

func (p *Pipe) sendLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.frameDur)
	defer ticker.Stop()

	var repeat [][]byte
	repeatLeft := 0
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}

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

		var err error
		if len(batch) == 0 {
			err = p.sendIdle()
		} else {
			err = p.sendBatch(batch)
		}
		if err != nil {
			if p.ctx.Err() != nil {
				return
			}
			log.Printf("[squares] send error: %v", err)
		}
	}
}

// takeBatch набирает посылок на maxData байт (с учётом 2-байтных префиксов).
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

func (p *Pipe) sendBatch(batch [][]byte) error {
	data := make([]byte, 0, p.maxData)
	for _, m := range batch {
		n := len(m)
		data = append(data, byte(n), byte(n>>8))
		data = append(data, m...)
	}
	payload := make([]byte, p.payloadBytes)
	binary.LittleEndian.PutUint32(payload[0:4], uint32(len(data)))
	binary.LittleEndian.PutUint32(payload[4:8], crc32.ChecksumIEEE(data))
	copy(payload[hdrLen:], data)
	p.stMsgs.Add(int64(len(batch)))
	return p.emit(payload, fmt.Sprintf("batch msgs=%d bytes=%d", len(batch), len(data)))
}

func (p *Pipe) sendIdle() error {
	p.idleSeq++
	seq := int(p.idleSeq)
	payload := make([]byte, p.payloadBytes)
	for r := 0; r < p.cfg.Rows; r++ {
		for c := 0; c < p.cfg.Cols; c++ {
			idx := r*p.cfg.Cols + c
			if idx < hdrCells {
				continue
			}
			val := byte((r + c + seq) % 16)
			set4Bit(payload, idx*4, val)
		}
	}
	return p.emit(payload, "")
}

func (p *Pipe) emit(payload []byte, label string) error {
	rgb := p.rgbBuf
	EncodeFrame(rgb, p.cfg.Width, p.cfg.Height, p.cfg.Cols, p.cfg.Rows, payload)
	frame, err := p.enc.Encode(rgb)
	if err != nil {
		return fmt.Errorf("squares: encode: %w", err)
	}
	if p.sharing {
		err = p.tunnel.WriteSharingVP8(frame)
	} else {
		err = p.tunnel.WriteRawVP8(frame)
	}
	if err != nil {
		return fmt.Errorf("squares: write: %w", err)
	}
	n := p.stFrames.Add(1)
	p.stBytes.Add(int64(len(frame)))
	if label != "" {
		log.Printf("[squares] SEND %s vp8=%d", label, len(frame))
	} else if n%300 == 1 {
		log.Printf("[squares] SEND idle seq=%d vp8=%d (frames=%d avg=%.0fB)",
			p.idleSeq, len(frame), n,
			float64(p.stBytes.Load())/float64(n))
	}
	return nil
}

func set4Bit(payload []byte, bitPos int, val byte) {
	bytePos := bitPos >> 3
	bitOff := uint(bitPos & 7)
	val &= 0x0F
	if bytePos < len(payload) {
		payload[bytePos] |= val << bitOff
	}
	if bitOff > 4 && bytePos+1 < len(payload) {
		payload[bytePos+1] |= val >> (8 - bitOff)
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

func (p *Pipe) recvLoop() {
	defer p.wg.Done()
	defer close(p.recvCh)
	frameCount := 0
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}
		raw, err := p.tunnel.Receive(p.ctx)
		if err != nil {
			if p.ctx.Err() == nil {
				log.Printf("[squares] recv loop exit: %v", err)
			}
			return
		}
		frameCount++
		rgb, err := p.dec.Decode(raw)
		if err != nil {
			log.Printf("[squares] recv #%d vp8=%d DECODE-FAIL: %v",
				frameCount, len(raw), err)
			continue
		}
		if rgb == nil {
			continue
		}
		payload := DecodeFrame(rgb, p.cfg.Width, p.cfg.Height,
			p.cfg.Cols, p.cfg.Rows, p.payloadBytes)
		if payload == nil {
			continue
		}
		length := binary.LittleEndian.Uint32(payload[0:4])
		if length == 0 {
			if frameCount <= 20 || frameCount%600 == 0 {
				log.Printf("[squares] recv #%d idle", frameCount)
			}
			continue
		}
		if int(length) > p.maxData {
			log.Printf("[squares] recv #%d BAD-LEN len=%d max=%d",
				frameCount, length, p.maxData)
			continue
		}
		crcGot := binary.LittleEndian.Uint32(payload[4:8])
		data := payload[hdrLen : hdrLen+int(length)]
		if crc32.ChecksumIEEE(data) != crcGot {
			log.Printf("[squares] recv #%d CRC-FAIL len=%d", frameCount, length)
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
				log.Printf("[squares] recv #%d truncated record (%d+%d > %d)",
					frameCount, off, mlen, len(data))
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
		log.Printf("[squares] recv #%d OK msgs=%d bytes=%d", frameCount, n, length)
	}
}

var _ transport.Pipe = (*Pipe)(nil)