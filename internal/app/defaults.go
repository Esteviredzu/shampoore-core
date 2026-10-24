package app

const IPv4Only = true

func defaultAdvanced() Values {
	return Values{
		"tun_name":       "shampoore0",
		"tun_mtu":        1500,
		"tun_inet4":      "172.19.0.1/30",
		"tun_auto_route": true,
		"tun_dns":        "1.1.1.1:53,8.8.8.8:53",

		"socks5_listen": "127.0.0.1:1080",
	}
}

func defaultSettings() *Settings {
	return &Settings{
		IPv4Only: IPv4Only,
		Advanced: NewValues(),
		Type:     "yandex",
	}
}