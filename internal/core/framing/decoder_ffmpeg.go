//go:build linux && !android

package framing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

const (
	decodeTimeout = 500 * time.Millisecond
	frameQueueLen = 32
)

type ffmpegDecoder struct {
	width, height int
	fps           int

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	frameCh  chan []byte
	errCh    chan error
	closed   bool
	dumped   bool
	statsIn  int
	statsOut int

	needKey  bool
	frameIdx uint64

	closeOnce sync.Once
}

var _ VideoDecoder = (*ffmpegDecoder)(nil)

func NewDecoder(width, height, fps int) (VideoDecoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("framing/decoder: bad dimensions")
	}
	if fps <= 0 {
		fps = 30
	}
	d := &ffmpegDecoder{
		width:   width,
		height:  height,
		fps:     fps,
		frameCh: make(chan []byte, frameQueueLen),
		errCh:   make(chan error, 1),
		needKey: true,
	}
	if err := d.start(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *ffmpegDecoder) start() error {
	cmd := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-f", "ivf", "-i", "-",
		"-vf", "scale=in_range=pc:out_range=pc", // гарантируем полный диапазон на выходе
		"-f", "rawvideo",
		"-pix_fmt", "yuv420p", // современный формат
		"-",
	)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("framing/decoder: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("framing/decoder: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("framing/decoder: start: %w", err)
	}
	d.cmd = cmd
	d.stdin = stdin
	d.stdout = stdout

	if err := d.writeIVFHeader(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	go d.readLoop()
	return nil
}

func (d *ffmpegDecoder) writeIVFHeader() error {
	h := make([]byte, 32)
	copy(h[0:4], "DKIF")
	binary.LittleEndian.PutUint16(h[6:8], 32)
	copy(h[8:12], "VP80")
	binary.LittleEndian.PutUint32(h[16:20], uint32(d.fps))
	binary.LittleEndian.PutUint32(h[20:24], 1)
	_, err := d.stdin.Write(h)
	if err != nil {
		log.Printf("[framing/decoder] write IVF header failed: %v", err)
	}
	return err
}

func (d *ffmpegDecoder) readLoop() {
	frameSize := d.width * d.height * 3 / 2
	defer func() {
		close(d.frameCh)
		close(d.errCh)
	}()
	for {
		yuv := make([]byte, frameSize)
		if _, err := io.ReadFull(d.stdout, yuv); err != nil {
			log.Printf("[framing/decoder] readLoop exit after %d frames: %v",
				d.statsOut, err)
			select {
			case d.errCh <- err:
			default:
			}
			return
		}
		d.mu.Lock()
		d.statsOut++
		n := d.statsOut
		d.mu.Unlock()
		if n == 1 {
			log.Printf("[framing/decoder] FIRST YUV frame produced")
		} else if n%50 == 0 {
			log.Printf("[framing/decoder] decoded %d frames, ffmpeg keeps up", n)
		}
		select {
		case d.frameCh <- yuv:
		case <-time.After(decodeTimeout):
			log.Printf("[framing/decoder] WARNING: frame dropped, "+
				"consumer stalled > %v", decodeTimeout)
		}
	}
}

func isVP8Keyframe(data []byte) bool {
	if len(data) < 1 {
		return false
	}
	return (data[0] & 0x01) == 0
}

func (d *ffmpegDecoder) Decode(vp8Frame []byte) ([]byte, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("framing/decoder: closed")
	}
	if d.needKey {
		if !isVP8Keyframe(vp8Frame) {
			d.mu.Unlock()
			return nil, nil
		}
		d.needKey = false
	}

	d.statsIn++
	inN := d.statsIn
	if inN <= 5 || inN%200 == 0 {
		log.Printf("[framing/decoder] in #%d size=%d (out=%d)",
			inN, len(vp8Frame), d.statsOut)
	}

	fh := make([]byte, 12)
	binary.LittleEndian.PutUint32(fh[0:4], uint32(len(vp8Frame)))
	binary.LittleEndian.PutUint64(fh[4:12], d.frameIdx)
	d.frameIdx++

	if _, err := d.stdin.Write(fh); err != nil {
		log.Printf("[framing/decoder] stdin write header err: %v", err)
		d.needKey = true
		d.mu.Unlock()
		return nil, fmt.Errorf("framing/decoder: write header: %w", err)
	}
	if _, err := d.stdin.Write(vp8Frame); err != nil {
		log.Printf("[framing/decoder] stdin write frame err: %v", err)
		d.needKey = true
		d.mu.Unlock()
		return nil, fmt.Errorf("framing/decoder: write frame: %w", err)
	}
	d.mu.Unlock()

	select {
	case yuv, ok := <-d.frameCh:
		if !ok {
			return nil, io.EOF
		}
		return yuv, nil
	case err, ok := <-d.errCh:
		if !ok || err == nil {
			return nil, io.EOF
		}
		return nil, err
	case <-time.After(decodeTimeout):
		return nil, nil
	}
}

func (d *ffmpegDecoder) Close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		d.mu.Unlock()
		if d.stdin != nil {
			_ = d.stdin.Close()
		}
		if d.cmd != nil && d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
			_ = d.cmd.Wait()
		}
	})
	return nil
}