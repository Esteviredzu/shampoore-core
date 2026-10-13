package runtime

import (
	"context"
	"io"
	"net"
	"net/netip"

	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

type Bridge struct {
	mux multiplexing.StreamMultiplexer
}

func NewBridge(m multiplexing.StreamMultiplexer) *Bridge {
	return &Bridge{mux: m}
}

func (b *Bridge) HandleTCP(ctx context.Context, conn net.Conn, dst netip.AddrPort) {
	defer conn.Close()

	stream, err := b.mux.OpenStream(ctx, map[string]any{
		"host": dst.Addr().String(),
		"port": int(dst.Port()),
	})
	if err != nil {
		return
	}
	defer stream.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(stream, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, stream); done <- struct{}{} }()
	<-done
}