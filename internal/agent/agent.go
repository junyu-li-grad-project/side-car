package agent

import (
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/earth"
	earth_gen "github.com/victor-leee/earth/github.com/victor-leee/earth"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/side-car"
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
	// cfg is local config
	cfg *config.Config
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
	earth.InitConnManager(func(cname string) (earth.ConnPool, error) {
		return earth.NewPool(earth.WithFactory(func() (net.Conn, error) {
			return net.Dial("tcp", cname)
		}), earth.WithInitSize(cfg.InitialPoolSize), earth.WithMaxSize(cfg.MaxPoolSize))
	})

	return &proxyAgentImpl{
		localLis:  localLis,
		remoteLis: remoteLis,
		cfg:       cfg,
	}, nil
}

func (a *proxyAgentImpl) WaitTermination(beforeCleanup, postCleanup Hook) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs
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
		msg, err := earth.FromReader(conn, blockRead)
		if err != nil {
			logrus.Errorf("[waitMsg] read earth failed: %v", err)
			continue
		}
		a.handle(msg, conn)
	}
}

func (a *proxyAgentImpl) handle(msg *earth.Message, conn net.Conn) {
	respMsg, err := a.handleRequest(msg, conn)
	if err != nil {
		logrus.Errorf("[agent.handle] handleRequest failed: %v", err)
		return
	}
	if err = a.handleResponse(respMsg, conn); err != nil {
		logrus.Errorf("[agent.handle] handleResponse failed: %v", err)
		return
	}
}

func (a *proxyAgentImpl) handleRequest(msg *earth.Message, conn net.Conn) (*earth.Message, error) {
	switch msg.Header.MessageType {
	case earth_gen.Header_SIDE_CAR_PROXY:
		return a.transferToSocket(msg)
	case earth_gen.Header_CONFIG_CENTER:
		return a.fetchConfig(msg)
	case earth_gen.Header_SET_USAGE:
		if err := earth.GlobalConnManager().Put(msg.Header.SenderServiceName, conn); err != nil {
			return earth.FromProtoMessage(&side_car.BaseResponse{
				Code: side_car.BaseResponse_CODE_ERROR,
			}, nil), fmt.Errorf("[agent.handleRequest]: %w", err)
		}

		return earth.FromProtoMessage(&side_car.BaseResponse{
			Code: side_car.BaseResponse_CODE_SUCCESS,
		}, nil), nil
	default:
		return nil, errors.New("[handleRequest] unknown receiver type")
	}
}

func (a *proxyAgentImpl) handleResponse(req *earth.Message, conn net.Conn) error {
	_, err := req.Write(conn)
	return err
}

// transferToSocket is used in two scenarios
// the first is that side-car communicates with local apps
// the second is that side-car communicates with other side-cars
// both cases the side-car will write earth and then block to read a earth
func (a *proxyAgentImpl) transferToSocket(msg *earth.Message) (*earth.Message, error) {
	var retMsg *earth.Message
	var retErr error
	earth.GlobalConnManager().Func(msg.Header.ReceiverServiceName, func(conn net.Conn) error {
		_, retErr = msg.Write(conn)
		if retErr != nil {
			logrus.Errorf("[transferToSocket] write earth to %v failed: %v", msg.Header.ReceiverServiceName, retErr)
			return nil
		}
		retMsg, retErr = earth.FromReader(conn, blockRead)
		if retErr != nil {
			logrus.Errorf("[transferToSocket] read earth from %v failed: %v", msg.Header.ReceiverServiceName, retErr)
			return nil
		}

		return nil
	})

	return retMsg, retErr
}

func (a *proxyAgentImpl) fetchConfig(msg *earth.Message) (*earth.Message, error) {
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

	return earth.FromBody(b, nil), nil
}

func blockRead(source io.Reader, size uint64) ([]byte, error) {
	b := make([]byte, size)
	var already uint64
	var inc int
	var err error
	for already < size {
		if inc, err = source.Read(b[already:]); err != nil {
			return nil, err
		}
		already += uint64(inc)
	}

	return b, nil
}
