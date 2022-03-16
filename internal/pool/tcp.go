package pool

import (
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"net"
	"time"
)

type tcpConnPool struct {
	factory    func() (net.Conn, error)
	initConn   int
	maxConn    int
	connChan   chan net.Conn
	ticketChan chan struct{}
}

type TCPConnOption func(pool *tcpConnPool)

func NewTcpConnPool(opts ...TCPConnOption) (ConnPool, error) {
	p := &tcpConnPool{}
	for _, opt := range opts {
		opt(p)
	}
	if err := p.init(); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *tcpConnPool) Get() (net.Conn, error) {
	select {
	case conn := <-p.connChan:
		return conn, nil
	default:
		// try to create one, if failed block wait the channel
		if !p.requestTicket() {
			return <-p.connChan, nil
		}

		return p.factory()
	}
}

func (p *tcpConnPool) Put(conn net.Conn) error {
	// before we put back the connection to the pool, we should check its status
	if p.isBrokenConn(conn) {
		// for each broken connection we allow one more creation
		p.createTicket()
		return errors.New("connection is broken")
	}
	p.connChan <- conn

	return nil
}

func (p *tcpConnPool) isBrokenConn(conn net.Conn) (broken bool) {
	defer func() {
		// set read deadline "never"
		if err := conn.SetReadDeadline(time.Time{}); err != nil {
			broken = true
		}
	}()

	if err := conn.SetReadDeadline(time.Now()); err != nil {
		logrus.Errorf("set deadline failed, err:%v", err)
		return true
	}
	b := make([]byte, 1)
	if _, err := conn.Read(b); err != nil {
		if p.isReadTimeoutErr(err) {
			return false
		}
		logrus.Errorf("found a broken connection: %v", err)
	}

	return true
}

func (p *tcpConnPool) isReadTimeoutErr(err error) bool {
	if netErr, ok := err.(*net.OpError); ok {
		return netErr.Timeout()
	}

	return false
}

func (p *tcpConnPool) init() error {
	if p.initConn > p.maxConn {
		return fmt.Errorf("initConn shouldn't exceed maxConn, actual is %d > %d", p.initConn, p.maxConn)
	}
	if p.factory == nil {
		return errors.New("must provide factory method to generate new connection")
	}
	if p.maxConn <= 0 {
		return errors.New("a pool should be allowed to contain resources, otherwise it's unnecessary to create one")
	}

	p.ticketChan = make(chan struct{}, p.maxConn)
	for i := 0; i < p.maxConn; i++ {
		p.createTicket()
	}

	p.connChan = make(chan net.Conn, p.maxConn)
	for i := 0; i < p.initConn; i++ {
		if !p.requestTicket() {
			return errors.New("request ticket to create connection failed")
		}
		conn, err := p.factory()
		if err != nil {
			// failed ? no worry, retry later
			p.createTicket()
			continue
		}
		p.connChan <- conn
	}

	return nil
}

func (p *tcpConnPool) requestTicket() (success bool) {
	select {
	case <-p.ticketChan:
		return true
	default:
		return false
	}
}

func (p *tcpConnPool) createTicket() {
	select {
	case p.ticketChan <- struct{}{}:
	default:
	}
}

func WithTCPFactory(f func() (net.Conn, error)) TCPConnOption {
	return func(pool *tcpConnPool) {
		pool.factory = f
	}
}

func WithInitSize(s int) TCPConnOption {
	return func(pool *tcpConnPool) {
		pool.initConn = s
	}
}

func WithMaxSize(s int) TCPConnOption {
	return func(pool *tcpConnPool) {
		pool.maxConn = s
	}
}
