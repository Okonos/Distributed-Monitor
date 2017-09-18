package intface

import "sync"

type concurrentPeerMap struct {
	mutex *sync.RWMutex
	_map  map[string]*Peer
}

func newConcurrentPeerMap() *concurrentPeerMap {
	return &concurrentPeerMap{
		mutex: &sync.RWMutex{},
		_map:  make(map[string]*Peer),
	}
}

func (m *concurrentPeerMap) get(key string) (peer *Peer, ok bool) {
	m.mutex.RLock()
	peer, ok = m._map[key]
	m.mutex.RUnlock()
	return
}

func (m *concurrentPeerMap) insert(key string, peer *Peer) {
	m.mutex.Lock()
	m._map[key] = peer
	m.mutex.Unlock()
}

func (m *concurrentPeerMap) len() int {
	m.mutex.RLock()
	ln := len(m._map)
	m.mutex.RUnlock()
	return ln
}

func (m *concurrentPeerMap) lockAndGetReference() map[string]*Peer {
	m.mutex.Lock()
	return m._map
}

func (m *concurrentPeerMap) unlock() {
	m.mutex.Unlock()
}
