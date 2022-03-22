package connection

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/pool"
	"net"
	"sync"
)

type UpdateType int8

const (
	InstanceCreate UpdateType = iota
	InstanceDelete
)

//go:generate mockgen -destination ../mock/manager/mock.go -source ./manager.go
// Manager is a central manager for managing known connections
type Manager interface {
	// Put add a connection to pool with key cname
	// recommend to call Func
	Put(cname string, conn net.Conn) error
	// Get returns a connection based on cname
	// recommend to call Func
	Get(cname string) (net.Conn, error)
	// UpdateServerInfo manages creation or deletion of connection pool
	// the purpose of this function is that Get and Put operations are heavily called
	// therefore we should avoid write lock in both functions, so we implement another function to do the work
	UpdateServerInfo(cname string, tp UpdateType) error
	// Func is a wrapper to Put&Get&UpdateServerInfo
	// it is recommended to use Func instead of handling Put/Get/UpdateServerInfo yourself unless
	// absolutely necessary
	Func(cname string, f func(conn net.Conn) error) error
}

type safeMap struct {
	mux         sync.RWMutex
	m           map[string]pool.ConnPool
	poolFactory pool.ConnPoolFactory
}

func (m *safeMap) insert(cname string) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	pl, err := m.poolFactory(cname)
	if err != nil {
		return err
	}
	m.m[cname] = pl

	return nil
}

func (m *safeMap) delete(cname string) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	pl, ok := m.m[cname]
	if !ok {
		return nil
	}
	delete(m.m, cname)

	return pl.Close()
}

func (m *safeMap) get(cname string) pool.ConnPool {
	m.mux.RLock()
	defer m.mux.RUnlock()

	if pl, ok := m.m[cname]; ok {
		return pl
	}

	return nil
}

type pooledConnManager struct {
	serviceID2Pool *safeMap
}

var connManager Manager

func InitConnManager(poolFactory pool.ConnPoolFactory) {
	connManager = &pooledConnManager{
		serviceID2Pool: &safeMap{
			poolFactory: poolFactory,
			m:           make(map[string]pool.ConnPool),
			mux:         sync.RWMutex{},
		},
	}
}

func GlobalConnManager() Manager {
	return connManager
}

func (p *pooledConnManager) Put(cname string, conn net.Conn) error {
	return p.serviceID2Pool.get(cname).Put(conn)
}

func (p *pooledConnManager) Get(cname string) (net.Conn, error) {
	pl := p.serviceID2Pool.get(cname)
	if pl == nil {
		return nil, nil
	}

	return pl.Get()
}

func (p *pooledConnManager) UpdateServerInfo(cname string, tp UpdateType) error {
	switch tp {
	case InstanceCreate:
		return p.serviceID2Pool.insert(cname)
	case InstanceDelete:
		return p.serviceID2Pool.delete(cname)
	default:
		return fmt.Errorf("invalid updateType: %+v", tp)
	}
}

func (p *pooledConnManager) Func(cName string, f func(conn net.Conn) error) error {
	conn, err := p.Get(cName)
	if err != nil {
		logrus.Warnf("[ConnManager.Func] get connection failed: %v", err)
		return err
	}
	if conn == nil {
		if err = p.UpdateServerInfo(cName, InstanceCreate); err != nil {
			logrus.Errorf("[ConnManager.Func] update server info failed: %v", err)
			return err
		}
		conn, err = p.Get(cName)
		if err != nil {
			logrus.Errorf("[ConnManager.Func] still get connection failed: %v", err)
			return err
		}
	}
	defer func() {
		if err = p.Put(cName, conn); err != nil {
			logrus.Errorf("[ConnManager.Func] put back connection failed: %v", err)
		}
	}()

	return f(conn)
}
