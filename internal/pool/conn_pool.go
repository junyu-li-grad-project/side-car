package pool

import "net"

type ConnPoolFactory func() (ConnPool, error)

//go:generate mockgen -destination ../mock/conn_pool/mock.go -source ./conn_pool.go
type ConnPool interface {
	Get() (net.Conn, error)
	Put(conn net.Conn) error
}
