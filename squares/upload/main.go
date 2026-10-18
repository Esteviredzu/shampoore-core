// FILE: cmd/vp8test/squares/main.go
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	"github.com/Esteviredzu/shampoore-core/internal/transport/vp8"
	telemost "github.com/Esteviredzu/shampoore-core/internal/transport/yandex_telemost"
)

// palette — 16 уровней серого, ПОЛНОСТЬЮ как в internal/transport/squares.
var palette = [16][3]byte{
	{8, 8, 8},
	{24, 24, 24},
	{40, 40, 40},
	{56, 56, 56},
	{72, 72, 72},
	{88, 88, 88},
	{104, 104, 104},
	{120, 120, 120},
	{136, 136, 136},
	{152, 152, 152},
	{168, 168, 168},
	{184, 184, 184},
	{200, 200, 200},
	{216, 216, 216},
	{232, 232, 232},
	{248, 248, 248},
}

type stats struct {
	payloadFrames atomic.Int64
	vp8Bytes      atomic.Int64
}

func main() {
	configPath := flag.String("c", "config_yandextelemost.json", "config file")
	filePath := flag.String("file", "test.bin", "file to send")
	width := flag.Int("w", 384, "video width")
	height := flag.Int("h", 216, "video height")
	cols := flag.Int("cols", 32, "grid columns")
	rows := flag.Int("rows", 18, "grid rows")
	fps := flag.Int("fps", 20, "frames per second")
	bitrate := flag.String("bitrate", "600K", "VP8 bitrate")
	rounds := flag.Int("rounds", 4, "full passes over all chunks")
	delay := flag.Int("delay", 5, "seconds to wait after connect before sending")
	flag.Parse()

	if *width%*cols != 0 || *height%*rows != 0 {
		log.Fatalf("grid must divide resolution")
	}
	cells := (*cols) * (*rows)
	if cells%2 != 0 {
		log.Fatalf("cells must be even")
	}
	payloadBytes := cells / 2
	dataBytes := payloadBytes - 8
	if dataBytes < 1 {
		log.Fatalf("dataBytes too small")
	}

	fileData, err := os.ReadFile(*filePath)
	if err != nil {
		log.Fatalf("read file: %v", err)
	}
	numChunks := (len(fileData) + dataBytes - 1) / dataBytes
	totalFrames := 4 + numChunks**rounds + 2
	log.Printf("file %s: %d bytes, %d chunks (data %dB/frame)",
		*filePath, len(fileData), numChunks, dataBytes)
	log.Printf("plan: 4 meta + %d chunks ×%d rounds + 2 meta = %d frames, ~%.0fs at %dfps",
		numChunks, *rounds, totalFrames, float64(totalFrames)/float64(*fps), *fps)

	s, err := app.ParseSource(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	tcfg := telemost.DefaultConfig()
	if data := s.TransportData(); len(data) > 0 {
		if err := json.Unmarshal(data, &tcfg); err != nil {
			log.Fatalf("telemost config: %v", err)
		}
	}
	if err := tcfg.Validate(); err != nil {
		log.Fatal(err)
	}

	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()

	logger := log.New(os.Stderr, "[squares] ", log.LstdFlags|log.Lmicroseconds)
	lg := logging.New("squares", true)

	tunnel, pipe, err := telemost.OpenTestTunnel(ctx, tcfg.ConferenceLink, lg)
	if err != nil {
		logger.Fatalf("telemost: %v", err)
	}
	defer func() { _ = pipe.Stop(context.Background()) }()
	tunnel.SetRawMode(true)

	logger.Printf("waiting %ds before sending", *delay)
	select {
	case <-time.After(time.Duration(*delay) * time.Second):
	case <-ctx.Done():
		return
	}

	cmd := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-f", "rawvideo", "-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", *width, *height),
		"-r", fmt.Sprintf("%d", *fps),
		"-i", "-",
		"-c:v", "libvpx",
		"-b:v", *bitrate,
		"-maxrate", *bitrate,
		"-bufsize", "2M",
		"-deadline", "realtime",
		"-cpu-used", "8",
		"-speed", "8",
		"-lag-in-frames", "0",
		"-auto-alt-ref", "0",
		"-error-resilient", "1",
		"-g", "1",
		"-an", "-threads", "1",
		"-f", "ivf", "-",
	)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		logger.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		logger.Fatal(err)
	}

	var st stats
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stdin.Close()
		if err := genFrames(ctx, stdin, *width, *height, *cols, *rows,
			*fps, *rounds, dataBytes, payloadBytes, fileData, &st, logger); err != nil && ctx.Err() == nil {
			logger.Printf("genFrames: %v", err)
		}
		logger.Printf("genFrames finished")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := readIVF(ctx, stdout, tunnel, &st, logger); err != nil && ctx.Err() == nil {
			logger.Printf("readIVF: %v", err)
		}
	}()

	<-ctx.Done()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	wg.Wait()
	_ = cmd.Wait()
	logger.Printf("exiting, sent %d payload frames", st.payloadFrames.Load())
}

func buildFrame(buf []byte, seq uint32, data []byte, dataBytes int) {
	padded := make([]byte, dataBytes)
	copy(padded, data)
	for i := len(data); i < dataBytes; i++ {
		padded[i] = 0xAA
	}
	binary.LittleEndian.PutUint32(buf[0:4], seq)
	binary.LittleEndian.PutUint32(buf[4:8], crc32.ChecksumIEEE(padded))
	copy(buf[8:8+dataBytes], padded)
}

func genFrames(
	ctx context.Context,
	w io.Writer,
	width, height, cols, rows, fps, rounds, dataBytes, payloadBytes int,
	fileData []byte,
	st *stats,
	logger *log.Logger,
) error {
	frameDur := time.Second / time.Duration(fps)
	frame := make([]byte, width*height*3)
	payload := make([]byte, payloadBytes)
	cellW := width / cols
	cellH := height / rows

	meta := make([]byte, dataBytes)
	binary.LittleEndian.PutUint64(meta[0:8], uint64(len(fileData)))

	send := func(seq uint32, data []byte) error {
		buildFrame(payload, seq, data, dataBytes)
		drawGrid(frame, width, cols, rows, cellW, cellH, payload)
		if _, err := w.Write(frame); err != nil {
			return err
		}
		st.payloadFrames.Add(1)
		select {
		case <-time.After(frameDur):
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}

	logger.Printf("[gen] meta x4 at start")
	for i := 0; i < 4; i++ {
		if err := send(0, meta); err != nil {
			return err
		}
	}

	start := time.Now()
	for round := 0; round < rounds; round++ {
		offset := 0
		frameIndex := uint32(1)
		for offset < len(fileData) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			n := dataBytes
			if offset+n > len(fileData) {
				n = len(fileData) - offset
			}
			if err := send(frameIndex, fileData[offset:offset+n]); err != nil {
				return err
			}
			offset += n
			frameIndex++
		}
		elapsed := time.Since(start)
		logger.Printf("[gen] round %d/%d done, elapsed=%.1fs", round+1, rounds, elapsed.Seconds())
	}

	logger.Printf("[gen] meta x2 at end")
	for i := 0; i < 2; i++ {
		if err := send(0, meta); err != nil {
			return err
		}
	}
	logger.Printf("[gen] done, %d rounds", rounds)
	return nil
}

func get4Bit(payload []byte, bitPos int) byte {
	bytePos := bitPos >> 3
	bitOff := uint(bitPos & 7)
	var v uint16
	if bytePos < len(payload) {
		v |= uint16(payload[bytePos])
	}
	if bytePos+1 < len(payload) {
		v |= uint16(payload[bytePos+1]) << 8
	}
	return byte((uint32(v) >> bitOff) & 0x0F)
}

func drawGrid(frame []byte, width, cols, rows, cellW, cellH int, payload []byte) {
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			val := get4Bit(payload, idx*4)
			col := palette[val]
			fillRect(frame, width, c*cellW, r*cellH, cellW, cellH, col)
		}
	}
}

func fillRect(frame []byte, width, x0, y0, w, h int, col [3]byte) {
	for y := y0; y < y0+h; y++ {
		rowOff := y * width * 3
		for x := x0; x < x0+w; x++ {
			off := rowOff + x*3
			frame[off+0] = col[0]
			frame[off+1] = col[1]
			frame[off+2] = col[2]
		}
	}
}

func readIVF(ctx context.Context, r io.Reader, tunnel *vp8.Tunnel, st *stats, logger *log.Logger) error {
	header := make([]byte, 32)
	if _, err := io.ReadFull(r, header); err != nil {
		return err
	}
	frameIndex := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		fh := make([]byte, 12)
		if _, err := io.ReadFull(r, fh); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return io.EOF
			}
			return err
		}
		size := binary.LittleEndian.Uint32(fh[0:4])
		if size == 0 {
			continue
		}
		frame := make([]byte, size)
		if _, err := io.ReadFull(r, frame); err != nil {
			return err
		}
		if err := tunnel.WriteRawVP8(frame); err != nil {
			continue
		}
		st.vp8Bytes.Add(int64(size))
		frameIndex++
		if frameIndex%100 == 0 {
			logger.Printf("[send] frame #%d size=%d", frameIndex, len(frame))
		}
	}
}