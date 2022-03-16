package main

import (
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/internal/sock"
)

func main() {
	if err := config.Init(); err != nil {
		fail(err)
	}
	if err := sock.Init(); err != nil {
		fail(err)
	}

	sock.Start()
}

func fail(err error) {
	logrus.Fatalf("start side car proxy failed: %v", err)
}
