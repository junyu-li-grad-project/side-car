package config

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	clientv3 "go.etcd.io/etcd/client/v3"
	"strings"
	"time"
)

var etcdClient *clientv3.Client

const (
	HierarchySeparator = "/"
)

func InitETCD(cfg *Config) error {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{fmt.Sprintf("%s:%d", cfg.ConfigCenterDNS, cfg.ConfigCenterPort)},
		DialTimeout: time.Second,
	})
	if err != nil {
		return err
	}
	etcdClient = cli

	return nil
}

func Put(ctx context.Context, key string, object interface{}) error {
	logrus.Infof("ETCDSet: key:%s, object:%+v", key, object)
	b, err := json.Marshal(object)
	if err != nil {
		return err
	}
	resp, err := etcdClient.Put(ctx, key, string(b))
	logrus.Infof("ETCDSet: response: %+v, err:%v", resp, err)
	if err != nil {
		return err
	}

	return nil
}

func Get(ctx context.Context, key string) (*clientv3.GetResponse, error) {
	logrus.Infof("ETCDGet: key: %s", key)
	resp, err := etcdClient.Get(ctx, key)
	logrus.Infof("ETCDGet: response: %+v, err:%v", resp, err)
	if err != nil {
		return nil, err
	}

	return resp, err
}

func Key(hierarchyKeys ...string) string {
	keysWithPrefix := make([]string, len(hierarchyKeys)+1)
	copy(keysWithPrefix[1:], hierarchyKeys)
	return strings.Join(keysWithPrefix, HierarchySeparator)
}
