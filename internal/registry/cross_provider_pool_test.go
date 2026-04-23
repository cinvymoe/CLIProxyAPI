package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCrossProviderPoolManager_RegisterPool(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pool := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
			{Provider: "provider2", Model: "model-b"},
		},
	}

	m.RegisterPool(pool)

	assert.True(t, m.IsPoolAlias("test-model"))
	assert.False(t, m.IsPoolAlias("other-model"))
}

func TestCrossProviderPoolManager_GetPoolProviders(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pool := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
			{Provider: "provider2", Model: "model-b"},
		},
	}

	m.RegisterPool(pool)

	providers := m.GetPoolProviders("test-model")
	assert.Equal(t, []string{"provider1", "provider2"}, providers)

	nilProviders := m.GetPoolProviders("nonexistent")
	assert.Nil(t, nilProviders)
}

func TestCrossProviderPoolManager_GetPoolModel(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pool := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
			{Provider: "provider2", Model: "model-b"},
		},
	}

	m.RegisterPool(pool)

	assert.Equal(t, "model-a", m.GetPoolModel("test-model", "provider1"))
	assert.Equal(t, "model-b", m.GetPoolModel("test-model", "provider2"))
	assert.Equal(t, "", m.GetPoolModel("test-model", "provider3"))
	assert.Equal(t, "", m.GetPoolModel("nonexistent", "provider1"))
}

func TestCrossProviderPoolManager_RegisterPools(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pools := []CrossProviderPool{
		{
			Alias: "model-1",
			Models: []CrossProviderPoolModel{
				{Provider: "p1", Model: "m1"},
			},
		},
		{
			Alias: "model-2",
			Models: []CrossProviderPoolModel{
				{Provider: "p2", Model: "m2"},
			},
		},
	}

	m.RegisterPools(pools)

	assert.True(t, m.IsPoolAlias("model-1"))
	assert.True(t, m.IsPoolAlias("model-2"))
	assert.Equal(t, "m1", m.GetPoolModel("model-1", "p1"))
	assert.Equal(t, "m2", m.GetPoolModel("model-2", "p2"))
}

func TestCrossProviderPoolManager_Clear(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pool := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
		},
	}

	m.RegisterPool(pool)
	assert.True(t, m.IsPoolAlias("test-model"))

	m.Clear()
	assert.False(t, m.IsPoolAlias("test-model"))
}

func TestCrossProviderPoolManager_GetPool(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pool := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
		},
	}

	m.RegisterPool(pool)

	result := m.GetPool("test-model")
	assert.NotNil(t, result)
	assert.Equal(t, "test-model", result.Alias)
	assert.Len(t, result.Models, 1)
	assert.Equal(t, "provider1", result.Models[0].Provider)

	nilResult := m.GetPool("nonexistent")
	assert.Nil(t, nilResult)
}

func TestCrossProviderPoolManager_GetAllPoolAliases(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pools := []CrossProviderPool{
		{Alias: "model-1", Models: []CrossProviderPoolModel{{Provider: "p1", Model: "m1"}}},
		{Alias: "model-2", Models: []CrossProviderPoolModel{{Provider: "p2", Model: "m2"}}},
		{Alias: "model-3", Models: []CrossProviderPoolModel{{Provider: "p3", Model: "m3"}}},
	}

	m.RegisterPools(pools)

	aliases := m.GetAllPoolAliases()
	assert.Len(t, aliases, 3)
}

func TestCrossProviderPoolManager_ThreadSafety(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pool := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
		},
	}

	done := make(chan bool)

	go func() {
		for i := 0; i < 1000; i++ {
			m.RegisterPool(pool)
			_ = m.IsPoolAlias("test-model")
			_ = m.GetPoolProviders("test-model")
			_ = m.GetPoolModel("test-model", "provider1")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 1000; i++ {
			_ = m.IsPoolAlias("test-model")
			_ = m.GetPoolProviders("test-model")
			_ = m.GetPoolModel("test-model", "provider1")
			m.Clear()
			m.RegisterPool(pool)
		}
		done <- true
	}()

	<-done
	<-done

	assert.True(t, m.IsPoolAlias("test-model"))
}
