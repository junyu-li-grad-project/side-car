package sock

import (
	"fmt"
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
// ConnManager is a central manager for managing known connections
type ConnManager interface {
	// Put add a connection to pool with key serviceID & instanceID
	// currently instanceID refers to "ip:port"
	Put(serviceID, instanceID string, conn net.Conn) error
	// Get returns a connection based on serviceID and instanceID
	Get(serviceID, instanceID string) (net.Conn, error)
	// UpdateServerInfo manages creation or deletion of connection pool
	// the purpose of this function is that Get and Put operations are heavily called
	// therefore we should avoid write lock in both functions, so we implement another function to do the work
	UpdateServerInfo(serviceID, instanceID string, tp UpdateType) error
}

type safeMap struct {
	mux         sync.RWMutex
	m           map[string]map[string]pool.ConnPool
	poolFactory pool.ConnPoolFactory
}

func (m *safeMap) insert(serviceID, instanceID string) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	if m.m[serviceID] == nil {
		m.m[serviceID] = make(map[string]pool.ConnPool)
	}
	pl, err := m.poolFactory()
	if err != nil {
		return err
	}
	m.m[serviceID][instanceID] = pl

	return nil
}

func (m *safeMap) delete(serviceID, instanceID string) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	if m.m[serviceID] == nil {
		return nil
	}
	pl := m.m[serviceID][instanceID]
	if pl == nil {
		return nil
	}
	m.m[serviceID][instanceID] = nil

	return pl.Close()
}

func (m *safeMap) get(serviceID, instanceID string) pool.ConnPool {
	m.mux.RLock()
	defer m.mux.RUnlock()

	return m.m[serviceID][instanceID]
}

type pooledConnManager struct {
	serviceID2Pool *safeMap
}

var connManager ConnManager

func InitConnManager(poolFactory pool.ConnPoolFactory) {
	connManager = &pooledConnManager{
		serviceID2Pool: &safeMap{
			poolFactory: poolFactory,
			m:           make(map[string]map[string]pool.ConnPool),
			mux:         sync.RWMutex{},
		},
	}
}

func GlobalConnManager() ConnManager {
	return connManager
}

func (p *pooledConnManager) Put(serviceID, instanceID string, conn net.Conn) error {
	return p.serviceID2Pool.get(serviceID, instanceID).Put(conn)
}

func (p *pooledConnManager) Get(serviceID, instanceID string) (net.Conn, error) {
	return p.serviceID2Pool.get(serviceID, instanceID).Get()
}

func (p *pooledConnManager) UpdateServerInfo(serviceID, instanceID string, tp UpdateType) error {
	switch tp {
	case InstanceCreate:
		return p.serviceID2Pool.insert(serviceID, instanceID)
	case InstanceDelete:
		return p.serviceID2Pool.delete(serviceID, instanceID)
	default:
		return fmt.Errorf("invalid updateType: %+v", tp)
	}
}
