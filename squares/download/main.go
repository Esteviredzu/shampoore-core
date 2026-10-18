// FILE: cmd/vp8test/squares_decode/main.go
package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"flag"
	"hash/crc32"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	telemost "github.com/Esteviredzu/shampoore-core/internal/transport/yandex_telemost"
)

const keepaliveSize = 1010

// palette — 16 уровней серого, как в internal/transport/squares.
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
	collected atomic.Int64
	skipped   atomic.Int64
}

func main() {
	configPath := flag.String("c", "config_yandextelemost.json", "config file")
	outPath := flag.String("out", "test_received.bin", "output file")
	width := flag.Int("w", 384, "video width")
	height := flag.Int("h", 216, "video height")
	cols := flag.Int("cols", 32, "grid columns")
	rows := flag.Int("rows", 18, "grid rows")
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

	logger := log.New(os.Stderr, "[decode] ", log.LstdFlags|log.Lmicroseconds)
	lg := logging.New("decode", true)

	tunnel, pipe, err := telemost.OpenTestTunnel(ctx, tcfg.ConferenceLink, lg)
	if err != nil {
		logger.Fatalf("telemost: %v", err)
	}
	defer func() { _ = pipe.Stop(context.Background()) }()
	tunnel.SetRawMode(true)

	logger.Printf("collecting VP8 frames into memory; Ctrl+C to decode+save")

	var st stats
	var frames [][]byte
	var framesMu sync.Mutex
	collectDone := make(chan struct{})

	go func() {
		defer close(collectDone)
		n := 0
		for {
			frame, err := tunnel.Receive(ctx)
			if err != nil {
				return
			}
			if len(frame) == keepaliveSize {
				st.skipped.Add(1)
				continue
			}
			cp := make([]byte, len(frame))
			copy(cp, frame)
			framesMu.Lock()
			frames = append(frames, cp)
			framesMu.Unlock()
			st.collected.Add(1)
			n++
			if n <= 5 || n%200 == 0 {
				logger.Printf("[collect] frame #%d size=%d", n, len(frame))
			}
		}
	}()

	<-ctx.Done()
	logger.Printf("stopped by signal, waiting collector...")
	<-collectDone

	framesMu.Lock()
	frameCount := len(frames)
	framesMu.Unlock()
	logger.Printf("collected %d VP8 frames (skipped %d keepalive)",
		frameCount, st.skipped.Load())

	if frameCount == 0 {
		logger.Printf("no frames collected")
		return
	}

	var ivfBuf bytes.Buffer
	header := make([]byte, 32)
	copy(header[0:4], []byte("DKIF"))
	binary.LittleEndian.PutUint16(header[6:8], 32)
	copy(header[8:12], []byte("VP80"))
	binary.LittleEndian.PutUint32(header[16:20], 30)
	binary.LittleEndian.PutUint32(header[20:24], 1)
	ivfBuf.Write(header)
	ts := uint64(0)
	for _, f := range frames {
		fh := make([]byte, 12)
		binary.LittleEndian.PutUint32(fh[0:4], uint32(len(f)))
		binary.LittleEndian.PutUint64(fh[4:12], ts)
		ivfBuf.Write(fh)
		ivfBuf.Write(f)
		ts++
	}
	logger.Printf("IVF buffer: %d bytes", ivfBuf.Len())

	logger.Printf("decoding with ffmpeg...")
	decodeCmd := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-f", "ivf", "-i", "-",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-",
	)
	decodeCmd.Stdin = bytes.NewReader(ivfBuf.Bytes())
	decodeCmd.Stderr = os.Stderr
	decOut, err := decodeCmd.StdoutPipe()
	if err != nil {
		logger.Fatalf("ffmpeg stdout: %v", err)
	}
	if err := decodeCmd.Start(); err != nil {
		logger.Fatalf("ffmpeg start: %v", err)
	}

	res, err := readRGB(context.Background(), decOut, *width, *height, *cols, *rows, payloadBytes, dataBytes, logger)
	_ = decodeCmd.Wait()
	if err != nil && err != io.EOF {
		logger.Printf("readRGB: %v", err)
	}
	if res == nil || res.fileSize == 0 {
		logger.Printf("no file data decoded")
		return
	}

	out := res.buf[:res.fileSize]
	if err := os.WriteFile(*outPath, out, 0644); err != nil {
		logger.Fatalf("write: %v", err)
	}
	sum := md5.Sum(out)

	logger.Printf("======== SUMMARY ========")
	logger.Printf("vp8 frames:     %d", frameCount)
	logger.Printf("decoded frames: %d", res.decodedFrames)
	logger.Printf("fileSize:       %d bytes", res.fileSize)
	logger.Printf("good chunks:    %d", res.goodChunks)
	logger.Printf("bad chunks:     %d (CRC fail)", res.badChunks)
	logger.Printf("missing chunks: %d", res.missing)
	logger.Printf("wrote:          %s", *outPath)
	logger.Printf("md5:            %x", sum)
	logger.Printf("=========================")
}

type recvResult struct {
	buf           []byte
	fileSize      int
	goodChunks    int
	badChunks     int
	missing       int
	decodedFrames int
}

func readRGB(ctx context.Context, r io.Reader, width, height, cols, rows, payloadBytes, dataBytes int, logger *log.Logger) (*recvResult, error) {
	frameSize := width * height * 3
	buf := make([]byte, frameSize)
	cellW := width / cols
	cellH := height / rows

	res := &recvResult{}
	received := make(map[uint32]bool)
	var maxSeq uint32
	frameIdx := 0

	for {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		default:
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return res, err
		}
		frameIdx++
		res.decodedFrames++

		payload, _ := decodeGrid(buf, width, cols, rows, cellW, cellH, payloadBytes)
		seq := binary.LittleEndian.Uint32(payload[0:4])
		crcGot := binary.LittleEndian.Uint32(payload[4:8])
		data := payload[8 : 8+dataBytes]
		crcCalc := crc32.ChecksumIEEE(data)

		if seq == 0 {
			if crcGot != crcCalc {
				continue
			}
			sz := binary.LittleEndian.Uint64(data[0:8])
			newSize := int(sz)
			if res.buf == nil {
				res.fileSize = newSize
				res.buf = make([]byte, res.fileSize)
				for i := range res.buf {
					res.buf[i] = 0xAA
				}
				logger.Printf("[read] meta: fileSize=%d (allocated)", res.fileSize)
			}
			continue
		}

		if res.fileSize == 0 {
			continue
		}
		if crcGot != crcCalc {
			res.badChunks++
			continue
		}

		offset := int(seq-1) * dataBytes
		if offset >= res.fileSize {
			continue
		}
		end := offset + dataBytes
		if end > res.fileSize {
			end = res.fileSize
		}
		copy(res.buf[offset:end], data[:end-offset])
		if !received[seq] {
			received[seq] = true
			res.goodChunks++
		}
		if seq > maxSeq {
			maxSeq = seq
		}
		if frameIdx%200 == 0 {
			logger.Printf("[read] decoded frame #%d seq=%d good=%d",
				frameIdx, seq, res.goodChunks)
		}
	}

	for seq := uint32(1); seq <= maxSeq; seq++ {
		if !received[seq] {
			res.missing++
		}
	}
	return res, nil
}

func decodeGrid(rgb []byte, width, cols, rows, cellW, cellH, payloadBytes int) ([]byte, int) {
	payload := make([]byte, payloadBytes)
	colorErrs := 0
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cx := c*cellW + cellW/2
			cy := r*cellH + cellH/2
			off := (cy*width + cx) * 3
			if off+2 >= len(rgb) {
				continue
			}
			l := lumaOf(rgb[off+0], rgb[off+1], rgb[off+2])
			idx := (l + 4) >> 4
			if idx > 15 {
				idx = 15
				colorErrs++
			}
			// «Ошибкой» считаем только выход яркости за диапазон
			// [0..255], что невозможно; поле оставлено для совместимости.
			cellIdx := r*cols + c
			bitPos := cellIdx * 4
			bytePos := bitPos >> 3
			bitOff := uint(bitPos & 7)
			if bytePos >= len(payload) {
				continue
			}
			payload[bytePos] |= byte((idx << bitOff) & 0xFF)
			if bitOff > 4 && bytePos+1 < len(payload) {
				payload[bytePos+1] |= byte(uint(idx) >> (8 - bitOff))
			}
		}
	}
	return payload, colorErrs
}

func lumaOf(r, g, b byte) int {
	return (int(r)*77 + int(g)*150 + int(b)*29) >> 8
}