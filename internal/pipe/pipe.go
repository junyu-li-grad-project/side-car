package pipe

import (
	"encoding/binary"
	"errors"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	side_car "github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/side-car"
	"google.golang.org/protobuf/proto"
	"net"
	"os"
	"os/signal"
	"syscall"
)

type TransitionMessage struct {
	HeaderLenBytes []byte
	Header         *side_car.Header
	RawHeader      []byte
	Body           []byte
}

var file *os.File

func Init() error {
	var err error
	fileName := config.GetConfig().PipeName
	if err = os.Remove(fileName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = syscall.Mkfifo(fileName, 0666); err != nil {
		return err
	}
	if file, err = os.OpenFile(fileName, os.O_RDWR|os.O_CREATE, os.ModeNamedPipe); err != nil {
		return err
	}
	logrus.Info("open pipe file succeed")

	return nil
}

func Start() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	msgChan := make(chan *TransitionMessage)
	go listenMsg(msgChan)

	select {
	case <-sigs:
		logrus.Infof("received signal to terminate process")
		close(msgChan)
		logrus.Infof("side car shut down gracefully")
	case msg := <-msgChan:
		if err := handle(msg); err != nil {
			logrus.Errorf("handle message failed: %v", err)
		}
	}
}

func handle(msg *TransitionMessage) error {
	conn, err := net.Dial("tcp", msg.Header.ReceiverServiceName)
	if err != nil {
		return err
	}
	if _, err = conn.Write(msg.HeaderLenBytes); err != nil {
		return err
	}
	if _, err = conn.Write(msg.RawHeader); err != nil {
		return err
	}
	if _, err = conn.Write(msg.Body); err != nil {
		return err
	}

	return nil
}

func listenMsg(msgChan chan<- *TransitionMessage) {
	for {
		// first block read the first 8 bytes, which is the length of the header
		headerLenBytes, err := blockRead(8)
		if err != nil {
			logrus.Fatalf("read header length failed: %v", err)
		}
		headerLen := binary.LittleEndian.Uint64(headerLenBytes)
		// then block read the header with length of headerLen
		headerBytes, err := blockRead(headerLen)
		if err != nil {
			logrus.Fatalf("read header failed: %v", err)
		}
		header := &side_car.Header{}
		if err = proto.Unmarshal(headerBytes, header); err != nil {
			logrus.Fatalf("unmarshal bytes to struct Header failed: %v", err)
		}
		// eventually read the body bytes
		body, err := blockRead(header.BodySize)
		if err != nil {
			logrus.Fatalf("read body failed: %v", err)
		}
		msgChan <- &TransitionMessage{
			HeaderLenBytes: headerLenBytes,
			Header:         header,
			RawHeader:      headerBytes,
			Body:           body,
		}
	}
}

func blockRead(size uint64) ([]byte, error) {
	var b []byte
	var already uint64
	var inc int
	var err error
	for already < size {
		if inc, err = file.Read(b); err != nil {
			return nil, err
		}
		already += uint64(inc)
	}

	return b, nil
}
