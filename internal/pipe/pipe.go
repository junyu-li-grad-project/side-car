package pipe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	side_car "github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/side-car"
	"google.golang.org/protobuf/proto"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// TransitionMessage holds the data transferred through local named pipe and sends them to other side cars
type TransitionMessage struct {
	HeaderLenBytes []byte
	Header         *side_car.Header
	RawHeader      []byte
	Body           []byte
}

// file is the named pipe used to transfer data between services and side-car
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
	switch msg.Header.ReceiverType {
	case side_car.Header_SIDE_CAR_PROXY:
		return transferToProxy(msg)
	case side_car.Header_CONFIG_CENTER:
		return fetchConfig(msg)
	default:
		return errors.New("unknown receiver type")
	}
}

func transferToProxy(msg *TransitionMessage) error {
	conn, err := net.Dial("tcp", msg.Header.ReceiverServiceName)
	if err != nil {
		logrus.Errorf("transfer to other side car , dial failed: %v", err)
		return err
	}
	if _, err = conn.Write(msg.HeaderLenBytes); err != nil {
		logrus.Errorf("transferToProxy write header length bytes failed: %v", err)
		return err
	}
	if _, err = conn.Write(msg.RawHeader); err != nil {
		logrus.Errorf("transferToProxy write header bytes failed: %v", err)
		return err
	}
	if _, err = conn.Write(msg.Body); err != nil {
		logrus.Errorf("transferToProxy write body bytes failed: %v", err)
		return err
	}

	return nil
}

func fetchConfig(msg *TransitionMessage) error {
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

	return transferToProxy(buildTransitionMessage(b))
}

func buildTransitionMessage(body []byte) *TransitionMessage {
	header := &side_car.Header{
		BodySize: uint64(len(body)),
	}
	headerBytes, _ := proto.Marshal(header)
	headerLenBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(headerLenBytes, uint64(len(headerBytes)))

	return &TransitionMessage{
		HeaderLenBytes: headerLenBytes,
		RawHeader:      headerBytes,
		Body:           body,
	}
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
