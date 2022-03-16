package pool

import "net"

type ConnPool interface {
	Get() (net.Conn, error)
	Put(conn net.Conn) error
}
