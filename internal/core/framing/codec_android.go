//go:build android

package framing

/*
#cgo LDFLAGS: -lmediandk -landroid
#include <media/NdkMediaCodec.h>
#include <media/NdkMediaFormat.h>
#include <stdlib.h>

typedef struct {
	int32_t width;
	int32_t height;
	int32_t stride;
	int32_t slice_height;
	int32_t color_format;
} shampoore_fmt_info;

static void shampoore_fmt_set_int32(AMediaFormat *f, const char *k, int32_t v) {
	(void)AMediaFormat_setInt32(f, k, v);
}

static void shampoore_fmt_set_string(AMediaFormat *f, const char *k, const char *v) {
	(void)AMediaFormat_setString(f, k, v);
}

static shampoore_fmt_info shampoore_read_fmt(AMediaCodec *codec) {
	shampoore_fmt_info info;
	info.width = 0;
	info.height = 0;
	info.stride = 0;
	info.slice_height = 0;
	info.color_format = 0;
	AMediaFormat *fmt = AMediaCodec_getOutputFormat(codec);
	if (fmt) {
		AMediaFormat_getInt32(fmt, "width", &info.width);
		AMediaFormat_getInt32(fmt, "height", &info.height);
		AMediaFormat_getInt32(fmt, "stride", &info.stride);
		AMediaFormat_getInt32(fmt, "slice-height", &info.slice_height);
		AMediaFormat_getInt32(fmt, "color-format", &info.color_format);
		AMediaFormat_delete(fmt);
	}
	return info;
}

static media_status_t shampoore_configure_encode(AMediaCodec *c, AMediaFormat *f) {
	return AMediaCodec_configure(c, f, NULL, NULL, AMEDIACODEC_CONFIGURE_FLAG_ENCODE);
}

static media_status_t shampoore_configure_decode(AMediaCodec *c, AMediaFormat *f) {
	return AMediaCodec_configure(c, f, NULL, NULL, 0);
}

static void shampoore_release_output(AMediaCodec *c, size_t idx) {
	(void)AMediaCodec_releaseOutputBuffer(c, idx, false);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	mimeVP8 = "video/x-vnd.on2.vp8"

	codecTimeoutUs      = 10000
	codecOverallTimeout = 3 * time.Second

	colorFormatSemiPlanar = 21
	colorFormatPlanar     = 19
	colorFormatFlexible   = 0x7F420888

	// Values from <media/NdkMediaCodec.h> (stable ABI).
	amcInfoTryAgainLater        = -1
	amcInfoOutputFormatChanged  = -2
	amcInfoOutputBuffersChanged = -3
	amcBufferFlagCodecConfig    = 2
)

var (
	cKeyMime           = C.CString("mime")
	cKeyWidth          = C.CString("width")
	cKeyHeight         = C.CString("height")
	cKeyBitRate        = C.CString("bit-rate")
	cKeyFrameRate      = C.CString("frame-rate")
	cKeyIFrameInterval = C.CString("i-frame-interval")
	cKeyColorFormat    = C.CString("color-format")
)

func parseBitrate(s string) int {
	if s == "" {
		return 4_000_000
	}
	s = strings.TrimSpace(s)
	mult := 1
	if strings.HasSuffix(s, "K") || strings.HasSuffix(s, "k") {
		mult = 1000
		s = s[:len(s)-1]
	} else if strings.HasSuffix(s, "M") || strings.HasSuffix(s, "m") {
		mult = 1_000_000
		s = s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 4_000_000
	}
	return n * mult
}

func planartoNV12(yuv []byte, width, height int) []byte {
	ySize := width * height
	uvSize := ySize / 2
	out := make([]byte, ySize+uvSize)
	copy(out, yuv[:ySize])
	u := yuv[ySize : ySize+uvSize/2]
	v := yuv[ySize+uvSize/2 : ySize+uvSize]
	uv := out[ySize:]
	for i := 0; i < uvSize/2; i++ {
		uv[i*2] = u[i]
		uv[i*2+1] = v[i]
	}
	return out
}

func nv12toPlanar(nv12 []byte, width, height int) []byte {
	ySize := width * height
	uvSize := ySize / 2
	out := make([]byte, ySize+uvSize)
	copy(out, nv12[:ySize])
	u := out[ySize : ySize+uvSize/2]
	v := out[ySize+uvSize/2 : ySize+uvSize]
	uv := nv12[ySize : ySize+uvSize]
	for i := 0; i < uvSize/2; i++ {
		u[i] = uv[i*2]
		v[i] = uv[i*2+1]
	}
	return out
}

// ---------- Encoder ----------

type androidEncoder struct {
	codec   *C.AMediaCodec
	width   int
	height  int
	fps     int
	bitrate int

	mu   sync.Mutex
	dead bool
	pts  int64

	closeOnce sync.Once
}

var _ VideoEncoder = (*androidEncoder)(nil)

func NewEncoder(width, height, fps int, bitrate string) (VideoEncoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("framing/android: bad dimensions")
	}
	if fps <= 0 {
		fps = 30
	}
	br := parseBitrate(bitrate)

	format := C.AMediaFormat_new()
	if format == nil {
		return nil, errors.New("framing/android: AMediaFormat_new failed")
	}
	defer C.AMediaFormat_delete(format)

	mimeCStr := C.CString(mimeVP8)
	defer C.free(unsafe.Pointer(mimeCStr))

	C.shampoore_fmt_set_string(format, cKeyMime, mimeCStr)
	C.shampoore_fmt_set_int32(format, cKeyWidth, C.int32_t(width))
	C.shampoore_fmt_set_int32(format, cKeyHeight, C.int32_t(height))
	C.shampoore_fmt_set_int32(format, cKeyBitRate, C.int32_t(br))
	C.shampoore_fmt_set_int32(format, cKeyFrameRate, C.int32_t(fps))
	C.shampoore_fmt_set_int32(format, cKeyIFrameInterval, C.int32_t(30))
	C.shampoore_fmt_set_int32(format, cKeyColorFormat, C.int32_t(colorFormatSemiPlanar))

	codec := C.AMediaCodec_createEncoderByType(mimeCStr)
	if codec == nil {
		return nil, errors.New("framing/android: no VP8 encoder on this device")
	}

	ret := C.shampoore_configure_encode(codec, format)
	if ret != 0 {
		C.AMediaCodec_delete(codec)
		return nil, fmt.Errorf("framing/android: encoder configure ret=%d", int(ret))
	}
	ret = C.AMediaCodec_start(codec)
	if ret != 0 {
		C.AMediaCodec_delete(codec)
		return nil, fmt.Errorf("framing/android: encoder start ret=%d", int(ret))
	}

	return &androidEncoder{
		codec:   codec,
		width:   width,
		height:  height,
		fps:     fps,
		bitrate: br,
	}, nil
}

func (e *androidEncoder) Encode(yuv []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dead {
		return nil, errors.New("framing/android: encoder closed")
	}

	want := e.width * e.height * 3 / 2
	if len(yuv) != want {
		return nil, fmt.Errorf("framing/android: yuv size %d, want %d", len(yuv), want)
	}
	nv12 := planartoNV12(yuv, e.width, e.height)

	deadline := time.Now().Add(codecOverallTimeout)

	feedDone := false
	for !feedDone {
		if time.Now().After(deadline) {
			return nil, errors.New("framing/android: encoder input timeout")
		}
		idx := C.AMediaCodec_dequeueInputBuffer(e.codec, codecTimeoutUs)
		if idx < 0 {
			continue
		}
		var inSize C.size_t
		inBuf := C.AMediaCodec_getInputBuffer(e.codec, C.size_t(idx), &inSize)
		if inBuf == nil {
			return nil, errors.New("framing/android: encoder getInputBuffer nil")
		}
		if int(inSize) < len(nv12) {
			return nil, fmt.Errorf("framing/android: input buf %d < %d", int(inSize), len(nv12))
		}
		dst := unsafe.Slice((*byte)(unsafe.Pointer(inBuf)), int(inSize))
		copy(dst, nv12)

		pts := e.pts
		e.pts += int64(1_000_000 / e.fps)

		ret := C.AMediaCodec_queueInputBuffer(
			e.codec, C.size_t(idx), 0, C.size_t(len(nv12)),
			C.uint64_t(pts), 0,
		)
		if ret != 0 {
			return nil, fmt.Errorf("framing/android: queueInputBuffer ret=%d", int(ret))
		}
		feedDone = true
	}

	for {
		if time.Now().After(deadline) {
			return nil, errors.New("framing/android: encoder output timeout")
		}
		var info C.AMediaCodecBufferInfo
		idx := int(C.AMediaCodec_dequeueOutputBuffer(e.codec, &info, codecTimeoutUs))
		if idx == amcInfoTryAgainLater ||
			idx == amcInfoOutputFormatChanged ||
			idx == amcInfoOutputBuffersChanged {
			continue
		}
		if idx < 0 {
			return nil, fmt.Errorf("framing/android: dequeueOutputBuffer %d", idx)
		}
		if uint32(info.flags)&amcBufferFlagCodecConfig != 0 {
			C.shampoore_release_output(e.codec, C.size_t(idx))
			continue
		}
		var outSize C.size_t
		outBuf := C.AMediaCodec_getOutputBuffer(e.codec, C.size_t(idx), &outSize)
		if outBuf == nil {
			C.shampoore_release_output(e.codec, C.size_t(idx))
			continue
		}
		sl := unsafe.Slice((*byte)(unsafe.Pointer(outBuf)), int(outSize))
		start := int(info.offset)
		end := start + int(info.size)
		if end > len(sl) {
			C.shampoore_release_output(e.codec, C.size_t(idx))
			return nil, errors.New("framing/android: output slice overflow")
		}
		frame := make([]byte, info.size)
		copy(frame, sl[start:end])
		C.shampoore_release_output(e.codec, C.size_t(idx))
		return frame, nil
	}
}

func (e *androidEncoder) Close() error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.dead = true
		codec := e.codec
		e.codec = nil
		e.mu.Unlock()
		if codec != nil {
			C.AMediaCodec_stop(codec)
			C.AMediaCodec_delete(codec)
		}
	})
	return nil
}

// ---------- Decoder ----------

type androidDecoder struct {
	codec  *C.AMediaCodec
	width  int
	height int

	mu   sync.Mutex
	dead bool
	pts  int64

	closeOnce sync.Once
}

var _ VideoDecoder = (*androidDecoder)(nil)

func NewDecoder(width, height, fps int) (VideoDecoder, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("framing/android: bad dimensions")
	}

	format := C.AMediaFormat_new()
	if format == nil {
		return nil, errors.New("framing/android: AMediaFormat_new failed")
	}
	defer C.AMediaFormat_delete(format)

	mimeCStr := C.CString(mimeVP8)
	defer C.free(unsafe.Pointer(mimeCStr))

	C.shampoore_fmt_set_string(format, cKeyMime, mimeCStr)
	C.shampoore_fmt_set_int32(format, cKeyWidth, C.int32_t(width))
	C.shampoore_fmt_set_int32(format, cKeyHeight, C.int32_t(height))

	codec := C.AMediaCodec_createDecoderByType(mimeCStr)
	if codec == nil {
		return nil, errors.New("framing/android: no VP8 decoder on this device")
	}

	ret := C.shampoore_configure_decode(codec, format)
	if ret != 0 {
		C.AMediaCodec_delete(codec)
		return nil, fmt.Errorf("framing/android: decoder configure ret=%d", int(ret))
	}
	ret = C.AMediaCodec_start(codec)
	if ret != 0 {
		C.AMediaCodec_delete(codec)
		return nil, fmt.Errorf("framing/android: decoder start ret=%d", int(ret))
	}

	return &androidDecoder{codec: codec, width: width, height: height}, nil
}

func (d *androidDecoder) Decode(vp8Frame []byte) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dead {
		return nil, errors.New("framing/android: decoder closed")
	}
	if len(vp8Frame) == 0 {
		return nil, nil
	}

	deadline := time.Now().Add(codecOverallTimeout)

	for {
		if time.Now().After(deadline) {
			return nil, errors.New("framing/android: decoder input timeout")
		}
		idx := C.AMediaCodec_dequeueInputBuffer(d.codec, codecTimeoutUs)
		if idx < 0 {
			continue
		}
		var inSize C.size_t
		inBuf := C.AMediaCodec_getInputBuffer(d.codec, C.size_t(idx), &inSize)
		if inBuf == nil {
			return nil, errors.New("framing/android: decoder getInputBuffer nil")
		}
		if int(inSize) < len(vp8Frame) {
			return nil, fmt.Errorf("framing/android: decoder input buf %d < %d", int(inSize), len(vp8Frame))
		}
		dst := unsafe.Slice((*byte)(unsafe.Pointer(inBuf)), int(inSize))
		copy(dst, vp8Frame)

		pts := d.pts
		d.pts += 33_000

		ret := C.AMediaCodec_queueInputBuffer(
			d.codec, C.size_t(idx), 0, C.size_t(len(vp8Frame)),
			C.uint64_t(pts), 0,
		)
		if ret != 0 {
			return nil, fmt.Errorf("framing/android: decoder queueInputBuffer ret=%d", int(ret))
		}
		break
	}

	for {
		if time.Now().After(deadline) {
			return nil, errors.New("framing/android: decoder output timeout")
		}
		var info C.AMediaCodecBufferInfo
		idx := int(C.AMediaCodec_dequeueOutputBuffer(d.codec, &info, codecTimeoutUs))
		if idx == amcInfoTryAgainLater {
			continue
		}
		if idx == amcInfoOutputBuffersChanged {
			continue
		}
		if idx == amcInfoOutputFormatChanged {
			continue
		}
		if idx < 0 {
			return nil, fmt.Errorf("framing/android: decoder dequeueOutputBuffer %d", idx)
		}
		if info.size == 0 {
			C.shampoore_release_output(d.codec, C.size_t(idx))
			continue
		}

		var outSize C.size_t
		outBuf := C.AMediaCodec_getOutputBuffer(d.codec, C.size_t(idx), &outSize)
		if outBuf == nil {
			C.shampoore_release_output(d.codec, C.size_t(idx))
			return nil, errors.New("framing/android: decoder getOutputBuffer nil")
		}

		fi := C.shampoore_read_fmt(d.codec)

		width := d.width
		height := d.height
		stride := d.width
		sliceHeight := d.height
		colorFormat := colorFormatSemiPlanar

		if int(fi.width) > 0 {
			width = int(fi.width)
		}
		if int(fi.height) > 0 {
			height = int(fi.height)
		}
		if int(fi.stride) > 0 {
			stride = int(fi.stride)
		}
		if int(fi.slice_height) > 0 {
			sliceHeight = int(fi.slice_height)
		}
		if int(fi.color_format) > 0 {
			colorFormat = int(fi.color_format)
		}

		sl := unsafe.Slice((*byte)(unsafe.Pointer(outBuf)), int(outSize))
		start := int(info.offset)
		end := start + int(info.size)
		if end > len(sl) {
			C.shampoore_release_output(d.codec, C.size_t(idx))
			return nil, errors.New("framing/android: decoder output overflow")
		}

		yuv, err := cropAndConvert(sl[start:end], width, height, stride, sliceHeight, colorFormat)
		C.shampoore_release_output(d.codec, C.size_t(idx))
		return yuv, err
	}
}

func (d *androidDecoder) Close() error {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.dead = true
		codec := d.codec
		d.codec = nil
		d.mu.Unlock()
		if codec != nil {
			C.AMediaCodec_stop(codec)
			C.AMediaCodec_delete(codec)
		}
	})
	return nil
}

func cropAndConvert(raw []byte, width, height, stride, sliceHeight, colorFormat int) ([]byte, error) {
	if stride <= 0 {
		stride = width
	}
	if sliceHeight <= 0 {
		sliceHeight = height
	}

	if stride == width && sliceHeight == height {
		switch colorFormat {
		case colorFormatSemiPlanar, colorFormatFlexible:
			if len(raw) < width*height*3/2 {
				return nil, fmt.Errorf("framing/android: short buffer %d", len(raw))
			}
			return nv12toPlanar(raw, width, height), nil
		case colorFormatPlanar:
			out := make([]byte, width*height*3/2)
			copy(out, raw[:len(out)])
			return out, nil
		default:
			if len(raw) < width*height*3/2 {
				return nil, fmt.Errorf("framing/android: short buffer %d", len(raw))
			}
			return nv12toPlanar(raw, width, height), nil
		}
	}

	ySize := stride * sliceHeight
	uvSize := stride * sliceHeight / 2
	if len(raw) < ySize+uvSize {
		return nil, fmt.Errorf("framing/android: short padded buffer %d < %d", len(raw), ySize+uvSize)
	}

	out := make([]byte, width*height*3/2)
	for y := 0; y < height; y++ {
		copy(out[y*width:(y+1)*width], raw[y*stride:y*stride+width])
	}

	switch colorFormat {
	case colorFormatSemiPlanar, colorFormatFlexible:
		uvSrc := raw[ySize : ySize+uvSize]
		u := out[width*height : width*height+width*height/4]
		v := out[width*height+width*height/4:]
		for y := 0; y < height/2; y++ {
			rowUV := uvSrc[y*stride : y*stride+width]
			for x := 0; x < width/2; x++ {
				u[y*(width/2)+x] = rowUV[x*2]
				v[y*(width/2)+x] = rowUV[x*2+1]
			}
		}
	default:
		uSrc := raw[ySize : ySize+uvSize/2]
		vSrc := raw[ySize+uvSize/2 : ySize+uvSize]
		uDst := out[width*height : width*height+width*height/4]
		vDst := out[width*height+width*height/4:]
		for y := 0; y < height/2; y++ {
			copy(uDst[y*(width/2):(y+1)*(width/2)], uSrc[y*stride:y*stride+width/2])
			copy(vDst[y*(width/2):(y+1)*(width/2)], vSrc[y*stride:y*stride+width/2])
		}
	}
	return out, nil
}