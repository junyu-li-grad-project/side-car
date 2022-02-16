package config

import "flag"

type Config struct {
	PipeName             string
	ConfigCenterHostname string
}

var cfg *Config

func Init() error {
	var pipeName, ccHostname string
	flag.StringVar(&pipeName, "pipe", "/tmp/sc.pipe", "specify the pipe key")
	flag.StringVar(&ccHostname, "cc", "cc", "specify config center host name")
	flag.Parse()
	cfg = &Config{
		PipeName:             pipeName,
		ConfigCenterHostname: ccHostname,
	}

	return nil
}

func GetConfig() *Config {
	return cfg
}
