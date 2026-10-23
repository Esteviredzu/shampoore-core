//go:build android

package runtime

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/engine"
)

var (
	androidEngineMu sync.Mutex
	androidEngineOn bool
)

func StartTUNFD(ctx context.Context, fd int, mtu int) error {
	if fd < 0 {
		return fmt.Errorf("tun/android: invalid fd %d", fd)
	}
	if mtu <= 0 {
		mtu = 1500
	}

	androidEngineMu.Lock()
	if androidEngineOn {
		androidEngineMu.Unlock()
		return fmt.Errorf("tun/android: engine already running")
	}
	androidEngineOn = true
	androidEngineMu.Unlock()

	defer func() {
		androidEngineMu.Lock()
		androidEngineOn = false
		androidEngineMu.Unlock()
	}()

	key := &engine.Key{
		Device:   fmt.Sprintf("fd://%d", fd),
		MTU:      mtu,
		Proxy:    "mux://",
		LogLevel: "error",
	}
	engine.Insert(key)
	engine.Start()
	defer engine.Stop()

	log.Printf("tun/android: started on fd=%d, mtu=%d", fd, mtu)
	<-ctx.Done()
	log.Println("tun/android: stopping")
	return nil
}