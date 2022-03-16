package sock

import (
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/internal/message"
	side_car "github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/side-car"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// localLis is used to receive data from other containers within the same pod
var localLis net.Listener

// remoteLis is used to receive data from other side-car proxy
var remoteLis net.Listener

func Init() error {
	var err error
	fileName := config.GetConfig().SockPath
	localLis, err = net.Listen("unix", fileName)
	if err != nil {
		logrus.Errorf("[Init] listen local sock failed: %v", err)
		return err
	}
	remoteLis, err = net.Listen("tcp", fmt.Sprintf(":%d", config.GetConfig().SideCarPort))
	if err != nil {
		logrus.Errorf("[Init] listen port %d failed: %v", config.GetConfig().SideCarPort, err)
		return err
	}

	return nil
}

func Start() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	msgChan := make(chan *message.Message)
	go waitConn(localLis, msgChan)
	go waitConn(remoteLis, msgChan)

	select {
	case <-sigs:
		logrus.Infof("[Start] received signal to terminate process")

		close(msgChan)
		if err := localLis.Close(); err != nil {
			logrus.Errorf("[Start] close local listener failed: %v", err)
		}
		if err := remoteLis.Close(); err != nil {
			logrus.Errorf("[Start] close remote listener failed: %v", err)
		}

		logrus.Infof("[Start] side car shut down gracefully")
	case msg := <-msgChan:
		if err := handle(msg); err != nil {
			logrus.Errorf("[Start] handle message failed: %v", err)
		}
	}
}

func waitConn(lis net.Listener, msgChan chan *message.Message) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			logrus.Errorf("[wantConn] accept conn failed: %v", err)
			continue
		}
		go waitMsg(conn, msgChan)
	}
}

func handle(msg *message.Message) error {
	switch msg.Header.MessageType {
	case side_car.Header_SIDE_CAR_PROXY:
		return transferToSocket(msg)
	case side_car.Header_CONFIG_CENTER:
		return fetchConfig(msg)
	default:
		return errors.New("[handle] unknown receiver type")
	}
}

func transferToSocket(msg *message.Message) error {

	return nil
}

func fetchConfig(msg *message.Message) error {
	configCenterURL :=
		fmt.Sprintf("http://%s/v2/keys/%s", config.GetConfig().ConfigCenterHostname, msg.Header.SenderServiceName)
	resp, err := http.Get(configCenterURL)
	if err != nil {
		logrus.Errorf("fetch config failed: %v", err)
		return err
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logrus.Errorf("fetch config close body failed: %v", err)
		}
	}()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Errorf("fetch config read body failed: %v", err)
		return err
	}

	transitionMsg := message.FromBody(b)
	transitionMsg.Header.ReceiverServiceName = msg.Header.SenderServiceName
	transitionMsg.Header.MessageType = side_car.Header_CONFIG_CENTER
	return transferToSocket(transitionMsg)
}

func waitMsg(source io.Reader, msgChan chan<- *message.Message) {
	for {
		msg, err := message.FromReader(source, blockRead)
		if err != nil {
			logrus.Errorf("[waitMsg] read message failed: %v", err)
			continue
		}
		msgChan <- msg
	}
}

func blockRead(source io.Reader, size uint64) ([]byte, error) {
	var b []byte
	var already uint64
	var inc int
	var err error
	for already < size {
		if inc, err = source.Read(b); err != nil {
			return nil, err
		}
		already += uint64(inc)
	}

	return b, nil
}
