package runtime

type TUNConfig struct {
	Name        string
	MTU         int
	Inet4       string
	AutoRoute   bool
	BypassHosts []string
	DNSServers  []string
}