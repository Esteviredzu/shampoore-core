package squares

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

type Encoder struct {
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

func NewEncoder(width, height, fps int, bitrate string) (*Encoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("squares/encoder: bad dimensions")
	}
	if fps <= 0 {
		fps = 30
	}
	if bitrate == "" {
		bitrate = "600K"
	}
	e := &Encoder{width: width, height: height, fps: fps, bitrate: bitrate}
	if err := e.start(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Encoder) start() error {
	args := []string{
		"-loglevel", "error",
		"-f", "rawvideo", "-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", e.width, e.height),
		"-r", fmt.Sprintf("%d", e.fps),
		"-i", "-",
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
		"-g", "30",           // keyframe раз в 30 кадров (~1 c при 30 fps)
		"-sc_threshold", "0", // отключить scenecut: keyframe строго по GOP
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
		return fmt.Errorf("squares/encoder: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("squares/encoder: stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("squares/encoder: start: %w", err)
	}
	e.cmd = cmd
	e.stdin = stdin
	e.stdout = stdout
	return nil
}

func (e *Encoder) Encode(rgb []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dead {
		return nil, errors.New("squares/encoder: closed")
	}
	want := e.width * e.height * 3
	if len(rgb) != want {
		return nil, fmt.Errorf("squares/encoder: bad rgb size %d, want %d", len(rgb), want)
	}
	if _, err := e.stdin.Write(rgb); err != nil {
		e.dead = true
		return nil, fmt.Errorf("squares/encoder: write rgb: %w", err)
	}
	if !e.headerRead {
		header := make([]byte, 32)
		if _, err := io.ReadFull(e.stdout, header); err != nil {
			e.dead = true
			return nil, fmt.Errorf("squares/encoder: read ivf header: %w", err)
		}
		if string(header[0:4]) != "DKIF" {
			e.dead = true
			return nil, fmt.Errorf("squares/encoder: bad ivf magic %q", header[0:4])
		}
		e.headerRead = true
	}
	fh := make([]byte, 12)
	if _, err := io.ReadFull(e.stdout, fh); err != nil {
		e.dead = true
		return nil, fmt.Errorf("squares/encoder: read frame header: %w", err)
	}
	size := binary.LittleEndian.Uint32(fh[0:4])
	if size == 0 || size > 1<<22 {
		e.dead = true
		return nil, fmt.Errorf("squares/encoder: bad frame size %d", size)
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(e.stdout, frame); err != nil {
		e.dead = true
		return nil, fmt.Errorf("squares/encoder: read frame: %w", err)
	}
	return frame, nil
}

func (e *Encoder) Close() error {
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