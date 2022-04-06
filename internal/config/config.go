package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type number interface {
	int8 | uint8 | int16 | uint16 | int32 | uint32 | int64 | uint64 | int
}

const (
	EnvSocketPath       = "SC_SOCK_PATH"
	EnvSideCarPort      = "SC_PORT"
	EnvConfigCenterDNS  = "SC_CONFIG_CENTER_DNS"
	EnvConfigCenterPort = "SC_CONFIG_CENTER_PORT"
	EnvCfgGetTimeout    = "SC_ETCD_GET_TIMEOUT"
	EnvInitialPoolSize  = "SC_POOL_INIT_SIZE"
	EnvMaxPoolSize      = "SC_POOL_MAX_SIZE"
)

type Config struct {
	// SockPath is the sock file used within the same pod
	SockPath string
	// SideCarPort is the port the side-car listens on, which is used to exchange data between side-cars.
	SideCarPort uint16
	// ConfigCenterDNS is used to stand for the config center
	ConfigCenterDNS string
	// ConfigCenterPort is the port the config center listens to
	ConfigCenterPort uint16
	// ConfigGetTimeout controls the maximum seconds when requesting for config from etcd
	ConfigGetTimeout int
	// InitialPoolSize is the initial size state of connection pools
	InitialPoolSize int
	// MaxPoolSize is the maximum size state of connection pools
	MaxPoolSize int
}

func (c *Config) ETCDGetTimeout() time.Duration {
	return time.Second * time.Duration(c.ConfigGetTimeout)
}

func Init() (*Config, error) {
	cfg := &Config{
		SockPath:         envStr(EnvSocketPath, "/tmp/sc.sock"),
		SideCarPort:      envInt[uint16](EnvSideCarPort, 80),
		ConfigCenterDNS:  envStr(EnvConfigCenterDNS, "config-etcd-infra"),
		ConfigCenterPort: envInt[uint16](EnvConfigCenterPort, 80),
		ConfigGetTimeout: envInt[int](EnvCfgGetTimeout, 3),
		InitialPoolSize:  envInt[int](EnvInitialPoolSize, 10),
		MaxPoolSize:      envInt[int](EnvMaxPoolSize, 50),
	}

	return cfg, nil
}

func envStr(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}

func envInt[N number](key string, defaultValue N) N {
	intStr := envStr(key, fmt.Sprintf("%d", defaultValue))
	intVal, err := strconv.ParseInt(intStr, 10, 64)
	if err != nil {
		panic(err)
	}

	return N(intVal)
}
