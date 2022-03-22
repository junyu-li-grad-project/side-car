package agent

import (
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/internal/connection"
	"github.com/victor-leee/side-car/internal/message"
	"github.com/victor-leee/side-car/internal/pool"
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

type Hook func()

// ProxyAgent is the entrance of the service mesh client
type ProxyAgent interface {
	// Start starts the mesh agent on the pod/VM/PM
	Start()
	// WaitTermination will gracefully shut down the agent
	WaitTermination(beforeCleanup, postCleanup Hook) error
}

type proxyAgentImpl struct {
	// localLis is used to receive data from other containers within the same pod
	localLis net.Listener
	// remoteLis is used to receive data from other side-car proxy
	remoteLis net.Listener
	// sigChan is used to receive termination signs sent from os to clean up resources before shutting down
	sigChan chan os.Signal
	cfg     *config.Config
}

func Init(cfg *config.Config) (ProxyAgent, error) {
	path := cfg.SockPath
	localLis, err := net.Listen("unix", path)
	if err != nil {
		logrus.Errorf("[Init] listen local agent failed: %v", err)
		return nil, err
	}
	remoteLis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.SideCarPort))
	if err != nil {
		logrus.Errorf("[Init] listen port %d failed: %v", cfg.SideCarPort, err)
		return nil, err
	}
	connection.InitConnManager(func(cname string) (pool.ConnPool, error) {
		return pool.New(pool.WithFactory(func() (net.Conn, error) {
			return net.Dial("tcp", cname)
		}), pool.WithInitSize(cfg.InitialPoolSize), pool.WithMaxSize(cfg.MaxPoolSize))
	})

	return &proxyAgentImpl{
		localLis:  localLis,
		remoteLis: remoteLis,
		cfg:       cfg,
	}, nil
}

func (a *proxyAgentImpl) WaitTermination(beforeCleanup, postCleanup Hook) error {
	<-a.sigChan
	beforeCleanup()
	if err := a.localLis.Close(); err != nil {
		logrus.Errorf("[WaitTermination] close local listener failed: %v", err)
		return err
	}
	if err := a.remoteLis.Close(); err != nil {
		logrus.Errorf("[WaitTermination] close remote listener failed: %v", err)
		return err
	}
	postCleanup()

	return nil
}

func (a *proxyAgentImpl) Start() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	a.sigChan = sigs
	go a.waitConn(a.localLis)
	go a.waitConn(a.remoteLis)
}

func (a *proxyAgentImpl) waitConn(lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				logrus.Warnf("the socket %v has been closed, please check if it's as expected", lis.Addr())
				// so the sock has been closed, no more conn we exit !
				break
			}
			logrus.Errorf("[waitConn] accept conn failed: %v", err)
			continue
		}
		go a.waitMsg(conn)
	}
}

func (a *proxyAgentImpl) waitMsg(conn net.Conn) {
	for {
		msg, err := message.FromReader(conn, blockRead)
		if err != nil {
			logrus.Errorf("[waitMsg] read message failed: %v", err)
			continue
		}
		a.handle(msg, conn)
	}
}

func (a *proxyAgentImpl) handle(msg *message.Message, conn net.Conn) {
	respMsg, err := a.handleRequest(msg, conn)
	if err != nil {
		logrus.Errorf("[uds.handle] handleRequest failed: %v", err)
		return
	}
	if err = a.handleResponse(respMsg, conn); err != nil {
		logrus.Errorf("[uds.handle] handleResponse failed: %v", err)
		return
	}
}

func (a *proxyAgentImpl) handleRequest(msg *message.Message, conn net.Conn) (*message.Message, error) {
	switch msg.Header.MessageType {
	case side_car.Header_SIDE_CAR_PROXY:
		return a.transferToSocket(msg)
	case side_car.Header_CONFIG_CENTER:
		return a.fetchConfig(msg)
	case side_car.Header_SET_USAGE:
		err := a.setUsage(msg, conn)
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

func (a *proxyAgentImpl) handleResponse(req *message.Message, conn net.Conn) error {
	_, err := req.Write(conn)
	return err
}

func (a *proxyAgentImpl) setUsage(msg *message.Message, conn net.Conn) error {
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
func (a *proxyAgentImpl) transferToSocket(msg *message.Message) (*message.Message, error) {
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

func (a *proxyAgentImpl) fetchConfig(msg *message.Message) (*message.Message, error) {
	configCenterURL :=
		fmt.Sprintf("http://%s/v2/keys/%s", a.cfg.ConfigCenterHostname, msg.Header.SenderServiceName)
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
