// FILE: cmd/squares-ping/main.go
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"log"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/Esteviredzu/shampoore-core/internal/app"
	"github.com/Esteviredzu/shampoore-core/internal/logging"
	_ "github.com/Esteviredzu/shampoore-core/internal/transport/yandex_telemost"
)

// ping-пакет: [0:4] id, [4:12] t0 (UnixNano), [12:] filler
const pingHdr = 12

func main() {
	configPath := flag.String("c", "client.json", "config file or shampoore:// URL")
	role := flag.String("role", "client", "client or server")
	count := flag.Int("count", 100, "number of pings (client only)")
	rate := flag.Int("rate", 4, "pings per second (client only)")
	size := flag.Int("size", 16, "ping payload size, >=12 (client only)")
	flag.Parse()

	s, err := app.ParseSource(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	lg := logging.New("ping", true)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rt, err := app.BuildRuntime(ctx, s, *role, lg)
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	// Транспорт стартуем вручную: мы обошли TunnelMultiplexer,
	// поэтому app.Runtime.Start() сам его не поднимет.
	if err := rt.Transport.Start(ctx); err != nil {
		log.Fatalf("transport start: %v", err)
	}
	defer rt.Transport.Stop(context.Background())
	if err := rt.Start(ctx); err != nil {
		log.Fatalf("runtime start: %v", err)
	}

	log.Printf("ping: started as %s, waiting 6s for WebRTC...", *role)
	select {
	case <-time.After(6 * time.Second):
	case <-ctx.Done():
		return
	}

	if *role == "server" {
		runServer(ctx, rt)
	} else {
		runClient(ctx, rt, *count, *rate, *size)
	}
}

func runServer(ctx context.Context, rt *app.Runtime) {
	log.Println("ping: SERVER MODE. Echoing...")
	n := 0
	for {
		data, err := rt.Transport.Receive(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("ping: server recv: %v", err)
			}
			return
		}
		if err := rt.Transport.Send(ctx, data); err != nil {
			log.Printf("ping: server send: %v", err)
			return
		}
		n++
		if n%50 == 0 {
			log.Printf("ping: echoed %d", n)
		}
	}
}

type result struct {
	mu    sync.Mutex
	rtts  map[uint32]time.Duration
	dupes int
	oob   int
	lastN int
	quiet int
}

func runClient(ctx context.Context, rt *app.Runtime, count, rate, size int) {
	if size < pingHdr {
		size = pingHdr
	}
	if rate < 1 {
		rate = 1
	}
	res := &result{rtts: make(map[uint32]time.Duration)}

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// --- приёмник ---
	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
		for {
			data, err := rt.Transport.Receive(runCtx)
			if err != nil {
				return
			}
			if len(data) < pingHdr {
				res.mu.Lock()
				res.oob++
				res.mu.Unlock()
				continue
			}
			id := binary.LittleEndian.Uint32(data[0:4])
			t0 := time.Unix(0, int64(binary.LittleEndian.Uint64(data[4:12])))
			rtt := time.Since(t0)
			res.mu.Lock()
			if _, ok := res.rtts[id]; ok {
				res.dupes++
			} else {
				res.rtts[id] = rtt
			}
			res.mu.Unlock()
		}
	}()

	log.Printf("ping: CLIENT MODE. count=%d rate=%d/s size=%dB", count, rate, size)
	interval := time.Second / time.Duration(rate)
	buf := make([]byte, size)
	start := time.Now()

sent:
	for i := 1; i <= count; i++ {
		select {
		case <-ctx.Done():
			break sent
		default:
		}
		binary.LittleEndian.PutUint32(buf[0:4], uint32(i))
		binary.LittleEndian.PutUint64(buf[4:12], uint64(time.Now().UnixNano()))
		for j := pingHdr; j < size; j++ {
			buf[j] = byte(i)
		}
		if err := rt.Transport.Send(ctx, buf); err != nil {
			log.Printf("ping: send #%d error: %v", i, err)
		}
		if i%25 == 0 {
			log.Printf("ping: sent %d/%d (%.0fs elapsed)", i, count, time.Since(start).Seconds())
		}
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			break sent
		}
	}

	// --- ждём «тишины»: 3 секунды без новых ответов, но не больше 20 ---
	log.Printf("ping: all sent, draining...")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		res.mu.Lock()
		n := len(res.rtts)
		res.mu.Unlock()
		if n == res.lastN {
			res.quiet++
			if res.quiet >= 10 { // 10 * 300ms = 3s тишины
				break
			}
		} else {
			res.quiet = 0
			res.lastN = n
		}
	}
	runCancel()
	<-recvDone

	// --- отчёт ---
	res.mu.Lock()
	recv := len(res.rtts)
	dupes, oob := res.dupes, res.oob
	var vals []time.Duration
	for _, v := range res.rtts {
		vals = append(vals, v)
	}
	res.mu.Unlock()
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })

	lost := count - recv
	var sum time.Duration
	for _, v := range vals {
		sum += v
	}
	pct := func(p float64) time.Duration {
		if len(vals) == 0 {
			return 0
		}
		i := int(float64(len(vals)-1) * p)
		return vals[i]
	}

	log.Printf("================= PING STATS =================")
	log.Printf("sent:      %d", count)
	log.Printf("recv:      %d", recv)
	log.Printf("lost:      %d (%.1f%%)", lost, float64(lost)/float64(count)*100)
	log.Printf("dupes:     %d", dupes)
	log.Printf("malformed: %d", oob)
	if recv > 0 {
		log.Printf("rtt min:   %v", vals[0])
		log.Printf("rtt avg:   %v", sum/time.Duration(recv))
		log.Printf("rtt p50:   %v", pct(0.50))
		log.Printf("rtt p90:   %v", pct(0.90))
		log.Printf("rtt p99:   %v", pct(0.99))
		log.Printf("rtt max:   %v", vals[len(vals)-1])
	}
	log.Printf("================================================")
}