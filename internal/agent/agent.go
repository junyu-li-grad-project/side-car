package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alibaba/sentinel-golang/api"
	"github.com/alibaba/sentinel-golang/core/base"
	"github.com/alibaba/sentinel-golang/core/flow"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/scrpc"
	scrpc_gen "github.com/victor-leee/scrpc/github.com/victor-leee/scrpc"
	"github.com/victor-leee/side-car/internal/config"
	config_backend "github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/config-backend"
	"github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/side-car"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
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
	localLis *scrpc.Listener
	// remoteLis is used to receive data from other side-car proxy
	remoteLis *scrpc.Listener
	// cfg is local config
	cfg *config.Config
	// configService is the interface for operating dynamic configurations
	configService config_backend.ConfigBackendService
}

type flowControlRules struct {
	Direction     config.FlowDirection `json:"direction"`
	TargetService string               `json:"target_service"`
	QPS           uint64               `json:"qps"`
}

func Init(cfg *config.Config) (ProxyAgent, error) {
	path := cfg.SockPath
	localLis, err := scrpc.Listen("unix", path, scrpc.WithListenerType(scrpc.ConnTypeSideCar2Local))
	if err != nil {
		logrus.Errorf("[Init] listen local agent failed: %v", err)
		return nil, err
	}
	remoteLis, err := scrpc.Listen("tcp", fmt.Sprintf(":%d", cfg.SideCarPort), scrpc.WithListenerType(scrpc.ConnTypeSideCar2SideCar))
	if err != nil {
		logrus.Errorf("[Init] listen port %d failed: %v", cfg.SideCarPort, err)
		return nil, err
	}
	scrpc.InitConnManager(func(cname string) (scrpc.ConnPool, error) {
		return scrpc.NewPool(scrpc.WithFactory(func() (*scrpc.Conn, error) {
			return scrpc.Dial("tcp", fmt.Sprintf("%s:%d", cname, cfg.SideCarPort))
		}), scrpc.WithInitSize(10), scrpc.WithMaxSize(50))
	})

	return &proxyAgentImpl{
		localLis:      localLis,
		remoteLis:     remoteLis,
		cfg:           cfg,
		configService: &config_backend.ConfigBackendServiceImpl{},
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
	go a.refreshFlowControlRules()
}

func (a *proxyAgentImpl) refreshFlowControlRules() {
	interval := time.Second
	timer := time.NewTimer(interval)
	for {
		<-timer.C
		ctx, cancel := context.WithTimeout(context.Background(), interval)
		configReq := &config_backend.GetConfigRequest{
			ServiceId:  a.cfg.ServiceID,
			ServiceKey: a.cfg.ServiceKey,
			Key:        config.FlowControlKey,
		}
		resp, err := a.configService.GetConfig(ctx, configReq)
		if err != nil {
			logrus.Errorf("failed to fetch flow control config:%v", err)
			continue
		}
		var rules []*flowControlRules
		if err = json.NewDecoder(bytes.NewReader([]byte(resp.Value))).Decode(&rules); err != nil {
			logrus.Errorf("decode flow control rules failed: %v", err)
			continue
		}
		sentinelRules := make([]*flow.Rule, 0, len(rules))
		for _, r := range rules {
			if r.Direction != config.Inbound && r.Direction != config.Outbound {
				logrus.Errorf("invalid flow direction:%v", r.Direction)
				continue
			}
			sentinelRules = append(sentinelRules, &flow.Rule{
				Resource:               a.buildFlowResourceName(r.Direction, a.cfg.ServiceID, r.TargetService),
				TokenCalculateStrategy: flow.Direct,
				ControlBehavior:        flow.Reject,
				Threshold:              float64(r.QPS),
			})
		}
		ok, err := flow.LoadRules(sentinelRules)
		if err != nil {
			logrus.Errorf("load rules to sentinel failed: %v", err)
			continue
		}
		if !ok {
			logrus.Warn("seems no config changed")
		}
		cancel()
	}
}

func (a *proxyAgentImpl) buildFlowResourceName(flowDir config.FlowDirection, from, to string) string {
	return strings.Join([]string{from, to, string(flowDir)}, ".")
}

func (a *proxyAgentImpl) waitConn(lis *scrpc.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			logrus.Errorf("[waitConn] accept conn failed: %v", err)
			continue
		}
		go a.waitMsg(conn)
	}
}

func (a *proxyAgentImpl) waitMsg(conn *scrpc.Conn) {
	for {
		msg, err := scrpc.FromReader(conn, blockRead)
		logrus.Infof("%+v", msg)
		if err != nil {
			logrus.Errorf("[waitMsg] read scrpc failed: %v", err)
			break
		}
		a.handle(msg, conn)
		if msg.Header.MessageType == scrpc_gen.Header_SET_USAGE {
			logrus.Info("exit block read because it's side-car to app connection")
			break
		}
	}
}

func (a *proxyAgentImpl) handle(msg *scrpc.Message, conn *scrpc.Conn) {
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

func (a *proxyAgentImpl) handleRequest(msg *scrpc.Message, conn *scrpc.Conn) (*scrpc.Message, error) {
	switch msg.Header.MessageType {
	case scrpc_gen.Header_SIDE_CAR_PROXY:
		return a.transferToSocket(msg)
	case scrpc_gen.Header_SET_USAGE:
		header := &scrpc_gen.Header{
			ReceiverMethodName: "__ack_set_usage",
		}
		if err := scrpc.GlobalConnManager().Put(msg.Header.SenderServiceName, conn); err != nil {
			return scrpc.FromProtoMessage(&side_car.BaseResponse{
				Code: side_car.BaseResponse_CODE_ERROR,
			}, header), fmt.Errorf("[agent.handleRequest]: %w", err)
		}

		return scrpc.FromProtoMessage(&side_car.BaseResponse{
			Code: side_car.BaseResponse_CODE_SUCCESS,
		}, header), nil
	default:
		return nil, errors.New("[handleRequest] unknown receiver type")
	}
}

func (a *proxyAgentImpl) handleResponse(req *scrpc.Message, conn *scrpc.Conn) error {
	_, err := req.Write(conn)
	return err
}

// transferToSocket is used in two scenarios
// the first is that side-car communicates with local apps
// the second is that side-car communicates with other side-cars
// both cases the side-car will write scrpc and then block to read a scrpc
func (a *proxyAgentImpl) transferToSocket(msg *scrpc.Message) (*scrpc.Message, error) {
	var retMsg *scrpc.Message
	var retErr error
	scrpc.GlobalConnManager().Func(msg.Header.ReceiverServiceName, func(conn *scrpc.Conn) error {
		// ------ throttling start
		var (
			e           *base.SentinelEntry
			b           error
			throttleKey string
		)
		if conn.Type == scrpc.ConnTypeSideCar2Local {
			throttleKey = a.buildFlowResourceName(config.Inbound, msg.Header.SenderServiceName, msg.Header.ReceiverServiceName)
		} else {
			throttleKey = a.buildFlowResourceName(config.Outbound, msg.Header.SenderServiceName, msg.Header.ReceiverServiceName)
		}
		e, b = api.Entry(throttleKey, api.WithTrafficType(base.Inbound))
		if b != nil {
			logrus.Info("throttled")
			retMsg = scrpc.FromBody(nil, &scrpc_gen.Header{
				MessageType: scrpc_gen.Header_THROTTLED,
			})
			return nil
		}
		// ------ throttling end

		logrus.Infof("transfer to %s", msg.Header.ReceiverServiceName)
		_, retErr = msg.Write(conn)
		logrus.Infof("transfer done, err:%v", retErr)
		if retErr != nil {
			logrus.Errorf("[transferToSocket] write scrpc to %v failed: %v", msg.Header.ReceiverServiceName, retErr)
			return nil
		}
		logrus.Info("now block read")
		retMsg, retErr = scrpc.FromReader(conn, blockRead)
		logrus.Infof("received response: %+v", retMsg)
		if retErr != nil {
			logrus.Errorf("[transferToSocket] read scrpc from %v failed: %v", msg.Header.ReceiverServiceName, retErr)
			return nil
		}
		if e != nil {
			logrus.Info("throttle pass")
			e.Exit()
		}

		return nil
	})

	return retMsg, retErr
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
