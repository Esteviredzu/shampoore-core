package framing

type VideoEncoder interface {
	Encode(rgb []byte) (vp8 []byte, err error)
	Close() error
}

type VideoDecoder interface {
	Decode(vp8 []byte) (rgb []byte, err error)
	Close() error
}