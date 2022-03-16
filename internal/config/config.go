package config

import "flag"

type Config struct {
	// SockPath is the sock file used within the same pod
	SockPath string
	// SideCarPort is the port the side-car listens on, which is used to exchange data between side-cars.
	SideCarPort uint16
	// ConfigCenterHostname is used to stand for the config center
	ConfigCenterHostname string
}

var cfg *Config

func Init() error {
	var sockPath, ccHostname string
	var sideCarPort uint
	flag.StringVar(&sockPath, "sock", "/tmp/sc.sock", "specify the sock path")
	flag.UintVar(&sideCarPort, "port", 56789, "specify the side-car port")
	flag.StringVar(&ccHostname, "cc", "cc", "specify config center host name")
	flag.Parse()
	cfg = &Config{
		SockPath:             sockPath,
		SideCarPort:          uint16(sideCarPort),
		ConfigCenterHostname: ccHostname,
	}

	return nil
}

func GetConfig() *Config {
	return cfg
}
