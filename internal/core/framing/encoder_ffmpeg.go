//go:build linux && !purego

package framing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

type ffmpegEncoder struct {
	width, height int
	fps           int
	bitrate       string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdout        io.ReadCloser
	headerRead    bool
	mu            sync.Mutex
	dead          bool
	closeOnce     sync.Once
}

var _ VideoEncoder = (*ffmpegEncoder)(nil)

func NewEncoder(width, height, fps int, bitrate string) (VideoEncoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("framing/encoder: bad dimensions")
	}
	if fps <= 0 {
		fps = 30
	}
	if bitrate == "" {
		bitrate = "600K"
	}
	e := &ffmpegEncoder{width: width, height: height, fps: fps, bitrate: bitrate}
	if err := e.start(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *ffmpegEncoder) start() error {
	args := []string{
		"-loglevel", "error",
		"-f", "rawvideo",
		"-pix_fmt", "yuv420p", // современный формат
		"-s", fmt.Sprintf("%dx%d", e.width, e.height),
		"-r", fmt.Sprintf("%d", e.fps),
		"-i", "-",
		"-vf", "scale=in_range=pc:out_range=pc", // явно задаём полный диапазон на входе и выходе
		"-c:v", "libvpx",
		"-b:v", e.bitrate,
		"-maxrate", e.bitrate,
		"-bufsize", "2M",
		"-deadline", "realtime",
		"-cpu-used", "8",
		"-speed", "8",
		"-lag-in-frames", "0",
		"-auto-alt-ref", "0",
		"-error-resilient", "1",
		"-g", "30",
		"-sc_threshold", "0",
		"-an",
		"-threads", "1",
		"-flush_packets", "1",
		"-f", "ivf",
		"-",
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("framing/encoder: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("framing/encoder: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("framing/encoder: start: %w", err)
	}
	e.cmd = cmd
	e.stdin = stdin
	e.stdout = stdout
	return nil
}

func (e *ffmpegEncoder) Encode(yuv []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dead {
		return nil, errors.New("framing/encoder: closed")
	}
	want := e.width * e.height * 3 / 2
	if len(yuv) != want {
		return nil, fmt.Errorf("framing/encoder: bad yuv size %d, want %d", len(yuv), want)
	}
	if _, err := e.stdin.Write(yuv); err != nil {
		e.dead = true
		return nil, fmt.Errorf("framing/encoder: write yuv: %w", err)
	}
	if !e.headerRead {
		header := make([]byte, 32)
		if _, err := io.ReadFull(e.stdout, header); err != nil {
			e.dead = true
			return nil, fmt.Errorf("framing/encoder: read ivf header: %w", err)
		}
		if string(header[0:4]) != "DKIF" {
			e.dead = true
			return nil, fmt.Errorf("framing/encoder: bad ivf magic %q", header[0:4])
		}
		e.headerRead = true
	}
	fh := make([]byte, 12)
	if _, err := io.ReadFull(e.stdout, fh); err != nil {
		e.dead = true
		return nil, fmt.Errorf("framing/encoder: read frame header: %w", err)
	}
	size := binary.LittleEndian.Uint32(fh[0:4])
	if size == 0 || size > 1<<22 {
		e.dead = true
		return nil, fmt.Errorf("framing/encoder: bad frame size %d", size)
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(e.stdout, frame); err != nil {
		e.dead = true
		return nil, fmt.Errorf("framing/encoder: read frame: %w", err)
	}
	return frame, nil
}

func (e *ffmpegEncoder) Close() error {
	e.closeOnce.Do(func() {
		if e.stdin != nil {
			_ = e.stdin.Close()
		}
		if e.cmd != nil && e.cmd.Process != nil {
			_ = e.cmd.Process.Kill()
			_ = e.cmd.Wait()
		}
	})
	return nil
}