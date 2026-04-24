package pool

import (
	"sync"
)

type Manager struct {
	mu    sync.RWMutex
	pools map[string]*CrossProviderPool
}

func NewManager() *Manager {
	return &Manager{
		pools: make(map[string]*CrossProviderPool),
	}
}

func (m *Manager) RegisterPool(pool CrossProviderPool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools[pool.Alias] = &pool
}

func (m *Manager) UnregisterPool(alias string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pools, alias)
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools = make(map[string]*CrossProviderPool)
}

func (m *Manager) GetPool(alias string) *CrossProviderPool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pools[alias]
}

func (m *Manager) IsPoolAlias(modelName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.pools[modelName]
	return exists
}

func (m *Manager) GetPoolProviders(alias string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, exists := m.pools[alias]
	if !exists {
		return nil
	}

	providers := make([]string, len(p.Models))
	for i, m := range p.Models {
		providers[i] = m.Provider
	}
	return providers
}

func (m *Manager) GetPoolModel(alias, provider string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, exists := m.pools[alias]
	if !exists {
		return ""
	}

	for _, m := range p.Models {
		if m.Provider == provider {
			return m.Model
		}
	}
	return ""
}

func (m *Manager) RegisterPools(pools []CrossProviderPool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pools = make(map[string]*CrossProviderPool, len(pools))
	for i := range pools {
		m.pools[pools[i].Alias] = &pools[i]
	}
}

func (m *Manager) GetAllPoolAliases() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	aliases := make([]string, 0, len(m.pools))
	for alias := range m.pools {
		aliases = append(aliases, alias)
	}
	return aliases
}
