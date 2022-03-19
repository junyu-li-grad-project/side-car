package sock

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/victor-leee/side-car/internal/pool"
	"net"
	"sync"
)

//go:generate mockgen -destination ../mock/manager/mock.go -source ./manager.go
// ConnManager is a central manager for managing known connections
type ConnManager interface {
	// Register add a connection to pool concurrent with key addr
	Register(serviceID string, conn net.Conn) error
	// Get returns a connection based on serviceID and load-balancing strategies
	Get(serviceID string) (net.Conn, error)
}

type safeMap struct {
	mux         sync.RWMutex
	m           map[string]pool.ConnPool
	poolFactory pool.ConnPoolFactory
}

func (m *safeMap) get(serviceID string) pool.ConnPool {
	m.mux.RLock()
	defer m.mux.RUnlock()

	return m.m[serviceID]
}

func (m *safeMap) put(serviceID string, conn net.Conn) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	if _, ok := m.m[serviceID]; !ok {
		pl, err := m.poolFactory()
		if err != nil {
			logrus.Errorf("[ConnManager.Put] create pool failed, err:%v", err)
			return err
		}
		m.m[serviceID] = pl
	}

	return m.m[serviceID].Put(conn)
}

type pooledConnManager struct {
	serviceID2Pool *safeMap
}

var connManager ConnManager

func InitConnManager(poolFactory pool.ConnPoolFactory) {
	connManager = &pooledConnManager{
		serviceID2Pool: &safeMap{
			poolFactory: poolFactory,
			m:           make(map[string]pool.ConnPool),
			mux:         sync.RWMutex{},
		},
	}
}

func GlobalConnManager() ConnManager {
	return connManager
}

func (p *pooledConnManager) Register(serviceID string, conn net.Conn) error {
	return p.serviceID2Pool.put(serviceID, conn)
}

func (p *pooledConnManager) Get(serviceID string) (net.Conn, error) {
	pl := p.serviceID2Pool.get(serviceID)
	if pl == nil {
		return nil, fmt.Errorf("no connection pool for %s yet", serviceID)
	}

	return pl.Get()
}
