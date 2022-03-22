package main

import (
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/internal/connection"
	"github.com/victor-leee/side-car/internal/pool"
	"github.com/victor-leee/side-car/internal/sock"
	"net"
)

func main() {
	if err := config.Init(); err != nil {
		fail(err)
	}
	if err := sock.Init(); err != nil {
		fail(err)
	}
	connection.InitConnManager(func(cname string) (pool.ConnPool, error) {
		return pool.New(pool.WithFactory(func() (net.Conn, error) {
			return net.Dial("tcp", cname)
		}), pool.WithInitSize(10), pool.WithMaxSize(50))
	})

	sock.Start()
}

func fail(err error) {
	logrus.Fatalf("start side car proxy failed: %v", err)
}
