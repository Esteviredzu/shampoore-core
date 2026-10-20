package framing

type VideoEncoder interface {
	Encode(yuv []byte) (vp8 []byte, err error)
	Close() error
}

type VideoDecoder interface {
	Decode(vp8 []byte) (yuv []byte, err error)
	Close() error
}