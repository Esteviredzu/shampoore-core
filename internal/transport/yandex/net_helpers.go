package yandex

import (
	"net"
	"time"
)

func dialTCP(target string, ipv4Only bool) (net.Conn, error) {
	network := "tcp"
	if ipv4Only {
		network = "tcp4"
	}
	return net.DialTimeout(network, target, 30*time.Second)
}