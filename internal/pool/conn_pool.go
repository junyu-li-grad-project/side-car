package pool

import "net"

//go:generate mockgen -destination ../mock/mock_conn_pool.go -source ./conn_pool.go
type ConnPool interface {
	Get() (net.Conn, error)
	Put(conn net.Conn) error
}
