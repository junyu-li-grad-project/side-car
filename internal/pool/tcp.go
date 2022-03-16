package pool

import (
	"errors"
	"fmt"
	"net"
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

}

func (p *tcpConnPool) Put(conn net.Conn) error {

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
