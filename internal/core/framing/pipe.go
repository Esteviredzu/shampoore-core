package framing

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

	"github.com/Esteviredzu/shampoore-core/internal/core/channel"
	"github.com/Esteviredzu/shampoore-core/internal/transport"
)

const (
	sendQueueCap   = 1024
	enqueueTimeout = 500 * time.Millisecond
)

type Config struct {
	Width, Height int
	Cols, Rows    int
	CellSize      int
	FPS           int
	Bitrate       string
	Copies        int
}

func DefaultConfig() Config {
	return Config{
		Cols:     60,
		Rows:     40,
		CellSize: 16,
		FPS:      30,
		Bitrate:  "600K",
		Copies:   1,
	}
}

type Pipe struct {
	ch  channel.MediaChannel
	cfg Config
	enc VideoEncoder
	dec VideoDecoder

	cells        int
	payloadBytes int
	maxMessage   int
	maxData      int
	hdrCells     int

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

	yuvBuf []byte

	stFrames atomic.Int64
	stBytes  atomic.Int64
	stMsgs   atomic.Int64
	stFull   atomic.Int64
}

func NewPipe(ch channel.MediaChannel, cfg Config) (*Pipe, error) {
	if ch == nil {
		return nil, errors.New("framing: nil media channel")
	}
	if err := requirePalette("NewPipe"); err != nil {
		return nil, err
	}
	if cfg.CellSize <= 0 {
		cfg.CellSize = 16
	}
	if cfg.Cols <= 0 {
		cfg.Cols = 60
	}
	if cfg.Rows <= 0 {
		cfg.Rows = 40
	}

	grid := Grid{
		CellSize: cfg.CellSize,
		Cols:     cfg.Cols,
		Rows:     cfg.Rows,
	}
	autoW, autoH, payloadBytes, maxData, maxMessage, err := grid.DeriveLayout()
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 {
		cfg.Width = autoW
	}
	if cfg.Height <= 0 {
		cfg.Height = autoH
	}
	if cfg.Width < cfg.Cols*cfg.CellSize {
		return nil, fmt.Errorf("framing: frame width %d < active %d",
			cfg.Width, cfg.Cols*cfg.CellSize)
	}
	if cfg.Height < cfg.Rows*cfg.CellSize {
		return nil, fmt.Errorf("framing: frame height %d < active %d",
			cfg.Height, cfg.Rows*cfg.CellSize)
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 30
	}
	if cfg.Bitrate == "" {
		cfg.Bitrate = "600K"
	}
	if cfg.Copies <= 0 {
		cfg.Copies = 1
	}

	return &Pipe{
		ch:           ch,
		cfg:          cfg,
		cells:        cfg.Cols * cfg.Rows,
		payloadBytes: payloadBytes,
		maxMessage:   maxMessage,
		maxData:      maxData,
		hdrCells:     HdrCells(),
		frameDur:     time.Second / time.Duration(cfg.FPS),
		sendCh:       make(chan []byte, sendQueueCap),
		recvCh:       make(chan []byte, 512),
	}, nil
}

func (p *Pipe) Start(ctx context.Context) error {
	if p.started {
		return nil
	}

	freezePalette()

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

	p.yuvBuf = make([]byte, YUVSize(p.cfg.Width, p.cfg.Height))
	uStart := p.cfg.Width * p.cfg.Height
	for i := uStart; i < len(p.yuvBuf); i++ {
		p.yuvBuf[i] = 128
	}

	p.started = true
	p.wg.Add(2)
	go p.recvLoop()
	go p.sendLoop()
	log.Printf("[framing] started: %dx%d cells, %d bit/cell (%d shades), payload=%dB/frame, max_msg=%d",
		p.cfg.Cols, p.cfg.Rows, BitsPerCell, len(PaletteY),
		p.payloadBytes, p.maxMessage)
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

func (p *Pipe) SessionID() string                                   { return "framing" }
func (p *Pipe) MaxMessageSize() int                                 { return p.maxMessage }
func (p *Pipe) DeleteMessage(ctx context.Context, key string) error { return nil }
func (p *Pipe) SetSharing(on bool)                                  { p.sharing = on }

func (p *Pipe) SendMessage(ctx context.Context, data []byte, key string) error {
	if !p.started {
		return errors.New("framing: pipe not started")
	}
	if len(data) == 0 {
		return nil
	}
	if len(data) > p.maxMessage {
		return fmt.Errorf("framing: message too large (%d > %d)",
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
			log.Printf("[framing] send queue FULL (%d bytes pending), "+
				"dropping %d bytes — KCP will retransmit (total drops=%d)",
				len(p.sendCh), len(cp), n)
		}
		return errors.New("framing: send queue full")
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
			log.Printf("[framing] send error: %v", err)
		}
	}
}

func (p *Pipe) takeBatch() [][]byte {
	var out [][]byte
	used := 0
	if p.carry != nil {
		out = append(out, p.carry)
		used += len(p.carry) + RecHdr
		p.carry = nil
	}
	for used < p.maxData {
		select {
		case m := <-p.sendCh:
			if used+len(m)+RecHdr > p.maxData {
				p.carry = m
				return out
			}
			out = append(out, m)
			used += len(m) + RecHdr
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
	copy(payload[HdrLen:], data)
	p.stMsgs.Add(int64(len(batch)))
	return p.emit(payload, fmt.Sprintf("batch msgs=%d bytes=%d", len(batch), len(data)))
}

func (p *Pipe) sendIdle() error {
	p.idleSeq++
	seq := int(p.idleSeq)
	payload := make([]byte, p.payloadBytes)
	shades := len(PaletteY)
	bits := BitsPerCell
	for r := 0; r < p.cfg.Rows; r++ {
		for c := 0; c < p.cfg.Cols; c++ {
			idx := r*p.cfg.Cols + c
			if idx < p.hdrCells {
				continue
			}
			val := byte((r + c + seq/8) % shades)
			setNBits(payload, idx*bits, bits, val)
		}
	}
	return p.emit(payload, "")
}

func (p *Pipe) emit(payload []byte, label string) error {
	yuv := p.yuvBuf
	EncodeFrameYUV(yuv, p.cfg.Width, p.cfg.Height, p.cfg.Cols, p.cfg.Rows,
		p.cfg.CellSize, payload)
	frame, err := p.enc.Encode(yuv)
	if err != nil {
		return fmt.Errorf("framing: encode: %w", err)
	}

	var werr error
	if p.sharing {
		type sharingWriter interface {
			WriteSharingVP8([]byte) error
		}
		if sw, ok := p.ch.(sharingWriter); ok {
			werr = sw.WriteSharingVP8(frame)
		} else {
			werr = p.ch.WriteRawVP8(frame)
		}
	} else {
		werr = p.ch.WriteRawVP8(frame)
	}
	if werr != nil {
		return fmt.Errorf("framing: write: %w", werr)
	}

	n := p.stFrames.Add(1)
	p.stBytes.Add(int64(len(frame)))
	if label != "" {
		log.Printf("[framing] SEND %s vp8=%d", label, len(frame))
	} else if n%300 == 1 {
		log.Printf("[framing] SEND idle seq=%d vp8=%d (frames=%d avg=%.0fB)",
			p.idleSeq, len(frame), n,
			float64(p.stBytes.Load())/float64(n))
	}
	return nil
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
		raw, err := p.ch.Receive(p.ctx)
		if err != nil {
			if p.ctx.Err() == nil {
				log.Printf("[framing] recv loop exit: %v", err)
			}
			return
		}
		frameCount++
		yuv, err := p.dec.Decode(raw)
		if err != nil {
			log.Printf("[framing] recv #%d vp8=%d DECODE-FAIL: %v",
				frameCount, len(raw), err)
			continue
		}
		if yuv == nil {
			continue
		}
		payload := DecodeFrameYUV(yuv, p.cfg.Width,
			p.cfg.Cols, p.cfg.Rows, p.cfg.CellSize, p.payloadBytes)
		if payload == nil {
			continue
		}
		length := binary.LittleEndian.Uint32(payload[0:4])
		if length == 0 {
			if frameCount <= 20 || frameCount%600 == 0 {
				log.Printf("[framing] recv #%d idle", frameCount)
			}
			continue
		}
		if int(length) > p.maxData {
			log.Printf("[framing] recv #%d BAD-LEN len=%d max=%d",
				frameCount, length, p.maxData)
			continue
		}
		crcGot := binary.LittleEndian.Uint32(payload[4:8])
		data := payload[HdrLen : HdrLen+int(length)]
		if crc32.ChecksumIEEE(data) != crcGot {
			log.Printf("[framing] recv #%d CRC-FAIL len=%d", frameCount, length)
			continue
		}
		off := 0
		n := 0
		for off+RecHdr <= len(data) {
			mlen := int(data[off]) | int(data[off+1])<<8
			off += RecHdr
			if mlen == 0 {
				break
			}
			if off+mlen > len(data) {
				log.Printf("[framing] recv #%d truncated record (%d+%d > %d)",
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
		log.Printf("[framing] recv #%d OK msgs=%d bytes=%d", frameCount, n, length)
	}
}

var _ transport.Pipe = (*Pipe)(nil)