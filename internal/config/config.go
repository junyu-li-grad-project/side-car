package config

import "flag"

type Config struct {
	SockPath             string
	ConfigCenterHostname string
}

var cfg *Config

func Init() error {
	var sockPath, ccHostname string
	flag.StringVar(&sockPath, "sock", "/tmp/sc.sock", "specify the sock path")
	flag.StringVar(&ccHostname, "cc", "cc", "specify config center host name")
	flag.Parse()
	cfg = &Config{
		SockPath:             sockPath,
		ConfigCenterHostname: ccHostname,
	}

	return nil
}

func GetConfig() *Config {
	return cfg
}
