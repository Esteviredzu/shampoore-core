package app

func defaultSettings() *Settings {
	return &Settings{
		Mode: ModeSOCKS5,

		SOCKS5: SOCKS5Config{
			Listen:   "127.0.0.1:1080",
			IPv4Only: true,
		},

		TUN: TUNConfig{
			Name:       "shampoore0",
			MTU:        1500,
			Inet4:      "172.19.0.1/30",
			AutoRoute:  true,
			DNSServers: []string{"1.1.1.1:53", "8.8.8.8:53"},
		},

		Advanced: Advanced{
			MuxFlushMs:        20,
			MuxFlushThreshold: 12000,
			ReliableWindow:    64,
			ReliableChunk:     0,
			PaddingEnabled:    true,
			PaddingTarget:     14000,
		},

		transportKind: "yandex",
	}
}