package squares

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
)

type Decoder struct {
	width, height int
	fps           int
	batchSize     int
	maxPending    int
	mu           sync.Mutex
	pending      [][]byte
	decodedQueue [][]byte
	closed       bool
}

func NewDecoder(width, height, fps int) (*Decoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("squares/decoder: bad dimensions")
	}
	if fps <= 0 {
		fps = 30
	}
	return &Decoder{
		width:      width,
		height:     height,
		fps:        fps,
		batchSize:  5,
		maxPending: 15,
	}, nil
}

func (d *Decoder) Decode(vp8Frame []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("squares/decoder: closed")
	}
	d.pending = append(d.pending, vp8Frame)
	if len(d.decodedQueue) == 0 &&
		(len(d.pending) >= d.batchSize || len(d.pending) >= d.maxPending) {
		batch := d.pending
		d.pending = nil
		rgbs, err := d.decodeBatch(batch)
		if err != nil {
			log.Printf("[squares/decoder] batch decode error (batch=%d): %v",
				len(batch), err)
			return nil, err
		}
		if len(rgbs) > 0 {
			d.decodedQueue = rgbs
		}
	}
	if len(d.decodedQueue) == 0 {
		return nil, nil
	}
	rgb := d.decodedQueue[0]
	d.decodedQueue = d.decodedQueue[1:]
	return rgb, nil
}

func isVP8Keyframe(data []byte) bool {
	if len(data) < 1 {
		return false
	}
	return (data[0] & 0x01) == 0
}

func (d *Decoder) decodeBatch(frames [][]byte) ([][]byte, error) {
	// С -g 1 каждый кадр — это keyframe.
	// Если WebRTC потерял RTP-пакет, кадр будет битым.
	// ffmpeg libvpx может закрашиться, если скормить ему битый keyframe.
	// Поэтому мы фильтруем: пропускаем всё, что не является keyframe.
	// KCP всё равно запросит ретрансмит потерянных данных.
	var validFrames [][]byte
	for _, f := range frames {
		if isVP8Keyframe(f) {
			validFrames = append(validFrames, f)
		}
	}
	if len(validFrames) == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	h := make([]byte, 32)
	copy(h[0:4], "DKIF")
	binary.LittleEndian.PutUint16(h[4:6], 0)
	binary.LittleEndian.PutUint16(h[6:8], 32)
	copy(h[8:12], "VP80")
	binary.LittleEndian.PutUint16(h[12:14], uint16(d.width))
	binary.LittleEndian.PutUint16(h[14:16], uint16(d.height))
	binary.LittleEndian.PutUint32(h[16:20], uint32(d.fps))
	binary.LittleEndian.PutUint32(h[20:24], 1)
	binary.LittleEndian.PutUint32(h[24:28], uint32(len(validFrames)))
	buf.Write(h)
	for i, f := range validFrames {
		fh := make([]byte, 12)
		binary.LittleEndian.PutUint32(fh[0:4], uint32(len(f)))
		binary.LittleEndian.PutUint64(fh[4:12], uint64(i))
		buf.Write(fh)
		buf.Write(f)
	}
	cmd := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-err_detect", "ignore_err",
		"-fflags", "+discardcorrupt",
		"-f", "ivf",
		"-i", "-",
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-an",
		"-",
	)
	cmd.Stdin = bytes.NewReader(buf.Bytes())
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("squares/decoder: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("squares/decoder: start: %w", err)
	}
	frameSize := d.width * d.height * 3
	var rgbs [][]byte
	for {
		rgb := make([]byte, frameSize)
		_, err := io.ReadFull(stdout, rgb)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return rgbs, fmt.Errorf("squares/decoder: read rgb: %w", err)
		}
		rgbs = append(rgbs, rgb)
	}
	_ = cmd.Wait()
	return rgbs, nil
}

func (d *Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}