package app

const IPv4Only = true

func defaultSettings() *Settings {
	return &Settings{
		Mode: ModeSOCKS5,

		IPv4Only: IPv4Only,

		SOCKS5: SOCKS5Config{
			Listen: "127.0.0.1:1080",
		},

		TUN: TUNConfig{
			Name:       "shampoore0",
			MTU:        1500,
			Inet4:      "172.19.0.1/30",
			AutoRoute:  true,
			DNSServers: []string{"1.1.1.1:53", "8.8.8.8:53"},
		},

		Advanced: NewValues(),

		transportKind: "yandex",
	}
}