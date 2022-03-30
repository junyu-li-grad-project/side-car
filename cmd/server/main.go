package main

import (
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/agent"
	"github.com/victor-leee/side-car/internal/config"
	"os"
)

func main() {
	os.Remove("/tmp/sc.sock")
	logrus.Info("into side-car")
	cfg, err := config.Init()
	if err != nil {
		fail(err)
	}
	logrus.Info("config init done")

	agt, err := agent.Init(cfg)
	if err != nil {
		fail(err)
	}
	logrus.Info("agent init done")
	agt.Start()
	fail(agt.WaitTermination(beforeHook, postHook))
}

func beforeHook() {
	logrus.Info("start cleaning up")
}

func postHook() {
	logrus.Info("cleaning up ended")
}

func fail(err error) {
	if err == nil {
		return
	}
	logrus.Fatalf("start side car proxy failed: %v", err)
}
