package runtime

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/xjasonlyu/tun2socks/v2/engine"

	"github.com/Esteviredzu/shampoore-core/internal/multiplexing"
)

const (
	iffTun    = 0x0001
	iffNoPI   = 0x1000
	tunsetiff = 0x400454ca
)

type ifreq struct {
	Name  [16]byte
	Flags uint16
	pad   [22]byte
}

type TUNConfig struct {
	Name        string
	MTU         int
	Inet4       string
	AutoRoute   bool
	BypassHosts []string
	DNSServers  []string
}

type originalRoute struct {
	gateway string
	device  string
}

func createTUN(name string, mtu int) (*os.File, error) {
	fd, err := syscall.Open("/dev/net/tun", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}

	var req ifreq
	copy(req.Name[:], name)
	req.Flags = iffTun | iffNoPI

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(tunsetiff),
		uintptr(unsafe.Pointer(&req)),
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("TUNSETIFF: %v", errno)
	}

	return os.NewFile(uintptr(fd), "/dev/net/tun"), nil
}

func StartTUN(ctx context.Context, cfg TUNConfig, m multiplexing.StreamMultiplexer) error {
	if cfg.Name == "" {
		cfg.Name = "shampoore0"
	}
	if cfg.MTU == 0 {
		cfg.MTU = 1500
	}
	if cfg.Inet4 == "" {
		cfg.Inet4 = "172.19.0.1/30"
	}
	if len(cfg.DNSServers) == 0 {
		cfg.DNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}
	}

	orig, err := getOriginalDefaultRoute()
	if err != nil {
		return fmt.Errorf("get original default route: %w", err)
	}
	log.Printf("tun: original default route: via %s dev %s", orig.gateway, orig.device)

	resolver := newResolver(cfg.DNSServers)
	bypassIPs := resolveHosts(resolver, cfg.BypassHosts)
	if len(bypassIPs) == 0 {
		return fmt.Errorf("tun: no bypass IPs resolved")
	}
	log.Printf("tun: bypass hosts -> %d IP(s): %v", len(bypassIPs), bypassIPs)

	file, err := createTUN(cfg.Name, cfg.MTU)
	if err != nil {
		return fmt.Errorf("create tun: %w", err)
	}
	defer file.Close()
	fd := file.Fd()

	cleanup, err := setupRoutes(cfg, orig, bypassIPs)
	if err != nil {
		return err
	}
	defer cleanup()

	// muxproxy уже настроен через muxproxy.SetMux в cmd/*/main.go
	_ = m

	key := &engine.Key{
		Device:   fmt.Sprintf("fd://%d", fd),
		MTU:      cfg.MTU,
		Proxy:    "mux://",
		LogLevel: "error",
	}
	engine.Insert(key)
	engine.Start()
	defer engine.Stop()

	log.Printf("tun: started on %s (fd=%d), mtu=%d, addr=%s",
		cfg.Name, fd, cfg.MTU, cfg.Inet4)

	<-ctx.Done()
	log.Println("tun: stopping")
	return nil
}

func setupRoutes(cfg TUNConfig, orig originalRoute, bypassIPs []string) (func(), error) {
	noop := func() {}

	if err := run("ip", "addr", "add", cfg.Inet4, "dev", cfg.Name); err != nil {
		return noop, fmt.Errorf("addr add: %w", err)
	}
	if err := run("ip", "link", "set", cfg.Name, "up"); err != nil {
		return noop, fmt.Errorf("link up: %w", err)
	}

	var addedBypass []string
	for _, ip := range bypassIPs {
		cidr := ip + "/32"
		err := run("ip", "route", "add", cidr, "via", orig.gateway, "dev", orig.device)
		if err != nil {
			if strings.Contains(err.Error(), "File exists") {
				log.Printf("tun: bypass %s already exists", cidr)
				addedBypass = append(addedBypass, cidr)
				continue
			}
			log.Printf("tun: bypass %s failed: %v", cidr, err)
			continue
		}
		addedBypass = append(addedBypass, cidr)
		log.Printf("tun: bypass %s via %s", cidr, orig.gateway)
	}

	if cfg.AutoRoute {
		if err := run("ip", "route", "add", "default", "dev", cfg.Name, "metric", "1"); err != nil {
			for _, cidr := range addedBypass {
				_ = run("ip", "route", "del", cidr)
			}
			return noop, fmt.Errorf("default route add: %w", err)
		}
		log.Printf("tun: default route now via %s", cfg.Name)
	}

	cleanup := func() {
		log.Println("tun: restoring routes...")
		if cfg.AutoRoute {
			_ = run("ip", "route", "del", "default", "dev", cfg.Name)
		}
		for _, cidr := range addedBypass {
			_ = run("ip", "route", "del", cidr)
		}
		_ = run("ip", "link", "delete", cfg.Name)
		log.Println("tun: routes restored")
	}
	return cleanup, nil
}

func getOriginalDefaultRoute() (originalRoute, error) {
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return originalRoute{}, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return originalRoute{}, fmt.Errorf("no default route")
	}
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	fields := strings.Fields(line)
	var r originalRoute
	for i, f := range fields {
		switch f {
		case "via":
			if i+1 < len(fields) {
				r.gateway = fields[i+1]
			}
		case "dev":
			if i+1 < len(fields) {
				r.device = fields[i+1]
			}
		}
	}
	if r.gateway == "" || r.device == "" {
		return originalRoute{}, fmt.Errorf("cannot parse default route: %q", line)
	}
	return r, nil
}

func newResolver(dnsServers []string) *net.Resolver {
	if len(dnsServers) == 0 {
		return net.DefaultResolver
	}
	primary := dnsServers[0]
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", primary)
		},
	}
}

func resolveHosts(resolver *net.Resolver, hosts []string) []string {
	seen := make(map[string]bool)
	var out []string

	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		ips, err := resolver.LookupIP(ctx, "ip4", h)
		cancel()

		if err != nil || len(ips) == 0 {
			log.Printf("tun: resolve %s via custom resolver failed: %v", h, err)
			ctx2, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
			ips, err = net.DefaultResolver.LookupIP(ctx2, "ip4", h)
			cancel2()
			if err != nil || len(ips) == 0 {
				log.Printf("tun: resolve %s via system resolver failed: %v", h, err)
				continue
			}
		}

		for _, ip := range ips {
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			s := v4.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
			log.Printf("tun: %s -> %s", h, s)
		}
	}
	return out
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)",
			name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}