package registry

import (
	"sync"
)

type CrossProviderPool struct {
	Alias  string                   `yaml:"alias" json:"alias"`
	Models []CrossProviderPoolModel `yaml:"models" json:"models"`
}

type CrossProviderPoolModel struct {
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model" json:"model"`
}

type CrossProviderPoolManager struct {
	mu    sync.RWMutex
	pools map[string]*CrossProviderPool
}

func NewCrossProviderPoolManager() *CrossProviderPoolManager {
	return &CrossProviderPoolManager{
		pools: make(map[string]*CrossProviderPool),
	}
}

func (m *CrossProviderPoolManager) RegisterPool(pool CrossProviderPool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools[pool.Alias] = &pool
}

func (m *CrossProviderPoolManager) UnregisterPool(alias string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pools, alias)
}

func (m *CrossProviderPoolManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools = make(map[string]*CrossProviderPool)
}

func (m *CrossProviderPoolManager) GetPool(alias string) *CrossProviderPool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pools[alias]
}

func (m *CrossProviderPoolManager) IsPoolAlias(modelName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.pools[modelName]
	return exists
}

func (m *CrossProviderPoolManager) GetPoolProviders(alias string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pool, exists := m.pools[alias]
	if !exists {
		return nil
	}

	providers := make([]string, len(pool.Models))
	for i, m := range pool.Models {
		providers[i] = m.Provider
	}
	return providers
}

func (m *CrossProviderPoolManager) GetPoolModel(alias, provider string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pool, exists := m.pools[alias]
	if !exists {
		return ""
	}

	for _, m := range pool.Models {
		if m.Provider == provider {
			return m.Model
		}
	}
	return ""
}

func (m *CrossProviderPoolManager) RegisterPools(pools []CrossProviderPool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pools = make(map[string]*CrossProviderPool, len(pools))
	for i := range pools {
		m.pools[pools[i].Alias] = &pools[i]
	}
}

func (m *CrossProviderPoolManager) GetAllPoolAliases() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	aliases := make([]string, 0, len(m.pools))
	for alias := range m.pools {
		aliases = append(aliases, alias)
	}
	return aliases
}
