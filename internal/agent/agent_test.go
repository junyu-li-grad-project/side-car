package agent

import (
	"github.com/stretchr/testify/assert"
	"github.com/victor-leee/side-car/internal/config"
	"github.com/victor-leee/side-car/internal/message"
	side_car "github.com/victor-leee/side-car/proto/gen/github.com/victor-leee/side-car"
	"google.golang.org/protobuf/proto"
	"net"
	"testing"
	"time"
)

func TestAgent(t *testing.T) {
	cfg, err := config.Init()
	assert.Nil(t, err)
	agt, err := Init(cfg)
	assert.Nil(t, err)
	agt.Start()
	go func() {
		time.Sleep(time.Second)
		conn, goErr := net.Dial("unix", cfg.SockPath)
		assert.Nil(t, goErr)
		setupMsg := message.FromProtoMessage(&side_car.InitConnectionReq{
			ConnectionType: side_car.InitConnectionReq_CONNECTION_TYPE_APP_TO_CAR,
		}, &side_car.Header{
			MessageType: side_car.Header_SET_USAGE,
		})
		_, goErr = setupMsg.Write(conn)
		assert.Nil(t, goErr)
		response, goErr := message.FromReader(conn, blockRead)
		assert.Nil(t, goErr)
		baseResponse := &side_car.BaseResponse{}
		assert.Nil(t, proto.Unmarshal(response.Body, baseResponse))
		assert.Equal(t, side_car.BaseResponse_CODE_SUCCESS, baseResponse.Code)
		t.Log("test passed")
	}()
	assert.Nil(t, agt.WaitTermination(func() {
		t.Log("start cleaning")
	}, func() {
		t.Log("end cleaning")
	}))
}
