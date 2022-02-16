package main

import (
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/internal/pipe"
)

func main() {
	if err := config.Init(); err != nil {
		fail(err)
	}
	if err := pipe.Init(); err != nil {
		fail(err)
	}

	pipe.Start()
}

func fail(err error) {
	logrus.Fatalf("start side car proxy failed: %v", err)
}
