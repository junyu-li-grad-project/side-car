package sock

import (
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/internal/connection"
	"github.com/victor-leee/side-car/internal/message"
	side_car "github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/side-car"
	"google.golang.org/protobuf/proto"
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
	go waitConn(localLis)
	go waitConn(remoteLis)

	<-sigs
	logrus.Infof("[Start] received signal to terminate process")

	if err := localLis.Close(); err != nil {
		logrus.Errorf("[Start] close local listener failed: %v", err)
	}
	if err := remoteLis.Close(); err != nil {
		logrus.Errorf("[Start] close remote listener failed: %v", err)
	}

	logrus.Infof("[Start] side car shut down gracefully")
}

func waitConn(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			logrus.Errorf("[wantConn] accept conn failed: %v", err)
			continue
		}
		go waitMsg(conn)
	}
}

func waitMsg(conn net.Conn) {
	for {
		msg, err := message.FromReader(conn, blockRead)
		if err != nil {
			logrus.Errorf("[waitMsg] read message failed: %v", err)
			continue
		}
		handle(msg, conn)
	}
}

func handle(msg *message.Message, conn net.Conn) {
	respMsg, err := handleRequest(msg, conn)
	if err != nil {
		logrus.Errorf("[uds.handle] handleRequest failed: %v", err)
		return
	}
	if err = handleResponse(respMsg, conn); err != nil {
		logrus.Errorf("[uds.handle] handleResponse failed: %v", err)
		return
	}
}

func handleRequest(msg *message.Message, conn net.Conn) (*message.Message, error) {
	switch msg.Header.MessageType {
	case side_car.Header_SIDE_CAR_PROXY:
		return transferToSocket(msg)
	case side_car.Header_CONFIG_CENTER:
		return fetchConfig(msg)
	case side_car.Header_SET_USAGE:
		err := setUsage(msg, conn)
		// anyway we should notify the app whether the connection is registered successfully or not
		resp := &side_car.BaseResponse{
			Code: side_car.BaseResponse_CODE_SUCCESS,
		}
		if err != nil {
			logrus.Errorf("[uds.handleRequest] setUsage failed: %v", err)
			resp = &side_car.BaseResponse{
				Code: side_car.BaseResponse_CODE_ERROR,
			}
		}

		return message.FromProtoMessage(resp), err
	default:
		return nil, errors.New("[handleRequest] unknown receiver type")
	}
}

func handleResponse(req *message.Message, conn net.Conn) error {
	_, err := req.Write(conn)
	return err
}

func setUsage(msg *message.Message, conn net.Conn) error {
	req := &side_car.InitConnectionReq{}
	if err := proto.Unmarshal(msg.Body, req); err != nil {
		logrus.Errorf("[uds.setUsage] unmarshal body failed: %v", err)
		return err
	}
	switch req.ConnectionType {
	case side_car.InitConnectionReq_CONNECTION_TYPE_APP_TO_CAR:
		// for this type we do nothing
	case side_car.InitConnectionReq_CONNECTION_TYPE_CAR_TO_APP:
		// for this type we put the connection to the pool
		return connection.GlobalConnManager().Put(msg.Header.SenderServiceName, conn)
	}

	return fmt.Errorf("unknown connection type %v", req.ConnectionType)
}

// transferToSocket is used in two scenarios
// the first is that side-car communicates with local apps
// the second is that side-car communicates with other side-cars
// both cases the side-car will write message and then block to read a message
func transferToSocket(msg *message.Message) (*message.Message, error) {
	var retMsg *message.Message
	var retErr error
	connection.GlobalConnManager().Func(msg.Header.ReceiverServiceName, func(conn net.Conn) error {
		_, retErr = msg.Write(conn)
		if retErr != nil {
			logrus.Errorf("[transferToSocket] write message to %v failed: %v", msg.Header.ReceiverServiceName, retErr)
			return nil
		}
		retMsg, retErr = message.FromReader(conn, blockRead)
		if retErr != nil {
			logrus.Errorf("[transferToSocket] read message from %v failed: %v", msg.Header.ReceiverServiceName, retErr)
			return nil
		}

		return nil
	})

	return retMsg, retErr
}

func fetchConfig(msg *message.Message) (*message.Message, error) {
	configCenterURL :=
		fmt.Sprintf("http://%s/v2/keys/%s", config.GetConfig().ConfigCenterHostname, msg.Header.SenderServiceName)
	resp, err := http.Get(configCenterURL)
	if err != nil {
		logrus.Errorf("fetch config failed: %v", err)
		return nil, err
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logrus.Errorf("fetch config close body failed: %v", err)
		}
	}()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Errorf("fetch config read body failed: %v", err)
		return nil, err
	}

	return message.FromBody(b), nil
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
