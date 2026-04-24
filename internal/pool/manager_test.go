package pool

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestManager_RegisterPool(t *testing.T) {
	m := NewManager()

	p := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
			{Provider: "provider2", Model: "model-b"},
		},
	}

	m.RegisterPool(p)

	assert.True(t, m.IsPoolAlias("test-model"))
	assert.False(t, m.IsPoolAlias("other-model"))
}

func TestManager_GetPoolProviders(t *testing.T) {
	m := NewManager()

	p := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
			{Provider: "provider2", Model: "model-b"},
		},
	}

	m.RegisterPool(p)

	providers := m.GetPoolProviders("test-model")
	assert.Equal(t, []string{"provider1", "provider2"}, providers)

	nilProviders := m.GetPoolProviders("nonexistent")
	assert.Nil(t, nilProviders)
}

func TestManager_GetPoolModel(t *testing.T) {
	m := NewManager()

	p := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
			{Provider: "provider2", Model: "model-b"},
		},
	}

	m.RegisterPool(p)

	assert.Equal(t, "model-a", m.GetPoolModel("test-model", "provider1"))
	assert.Equal(t, "model-b", m.GetPoolModel("test-model", "provider2"))
	assert.Equal(t, "", m.GetPoolModel("test-model", "provider3"))
	assert.Equal(t, "", m.GetPoolModel("nonexistent", "provider1"))
}

func TestManager_RegisterPools(t *testing.T) {
	m := NewManager()

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

func TestManager_Clear(t *testing.T) {
	m := NewManager()

	p := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
		},
	}

	m.RegisterPool(p)
	assert.True(t, m.IsPoolAlias("test-model"))

	m.Clear()
	assert.False(t, m.IsPoolAlias("test-model"))
}

func TestManager_GetPool(t *testing.T) {
	m := NewManager()

	p := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
		},
	}

	m.RegisterPool(p)

	result := m.GetPool("test-model")
	assert.NotNil(t, result)
	assert.Equal(t, "test-model", result.Alias)
	assert.Len(t, result.Models, 1)
	assert.Equal(t, "provider1", result.Models[0].Provider)

	nilResult := m.GetPool("nonexistent")
	assert.Nil(t, nilResult)
}

func TestManager_GetAllPoolAliases(t *testing.T) {
	m := NewManager()

	pools := []CrossProviderPool{
		{Alias: "model-1", Models: []CrossProviderPoolModel{{Provider: "p1", Model: "m1"}}},
		{Alias: "model-2", Models: []CrossProviderPoolModel{{Provider: "p2", Model: "m2"}}},
		{Alias: "model-3", Models: []CrossProviderPoolModel{{Provider: "p3", Model: "m3"}}},
	}

	m.RegisterPools(pools)

	aliases := m.GetAllPoolAliases()
	assert.Len(t, aliases, 3)
}

func TestManager_ThreadSafety(t *testing.T) {
	m := NewManager()

	p := CrossProviderPool{
		Alias: "test-model",
		Models: []CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
		},
	}

	done := make(chan bool)

	go func() {
		for i := 0; i < 1000; i++ {
			m.RegisterPool(p)
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
			m.RegisterPool(p)
		}
		done <- true
	}()

	<-done
	<-done

	assert.True(t, m.IsPoolAlias("test-model"))
}

func TestResolver_IsPool(t *testing.T) {
	m := NewManager()
	r := NewResolver(m)

	p := CrossProviderPool{
		Alias: "my-pool",
		Models: []CrossProviderPoolModel{
			{Provider: "p1", Model: "m1"},
		},
	}
	m.RegisterPool(p)

	assert.True(t, r.IsPool("my-pool"))
	assert.False(t, r.IsPool("no-such-pool"))
}

func TestResolver_ResolveModel(t *testing.T) {
	m := NewManager()
	r := NewResolver(m)

	p := CrossProviderPool{
		Alias: "my-pool",
		Models: []CrossProviderPoolModel{
			{Provider: "p1", Model: "resolved-a"},
			{Provider: "p2", Model: "resolved-b"},
		},
	}
	m.RegisterPool(p)

	resolved, isPool := r.ResolveModel("my-pool", "p1")
	assert.True(t, isPool)
	assert.Equal(t, "resolved-a", resolved)

	resolved, isPool = r.ResolveModel("my-pool", "p2")
	assert.True(t, isPool)
	assert.Equal(t, "resolved-b", resolved)

	resolved, isPool = r.ResolveModel("my-pool", "p3")
	assert.True(t, isPool)
	assert.Equal(t, "", resolved)

	resolved, isPool = r.ResolveModel("not-a-pool", "p1")
	assert.False(t, isPool)
	assert.Equal(t, "", resolved)
}

func TestResolver_PoolProviders(t *testing.T) {
	m := NewManager()
	r := NewResolver(m)

	p := CrossProviderPool{
		Alias: "my-pool",
		Models: []CrossProviderPoolModel{
			{Provider: "p1", Model: "m1"},
			{Provider: "p2", Model: "m2"},
		},
	}
	m.RegisterPool(p)

	assert.Equal(t, []string{"p1", "p2"}, r.PoolProviders("my-pool"))
	assert.Nil(t, r.PoolProviders("not-a-pool"))
}

func TestSanitizePools(t *testing.T) {
	pools := []CrossProviderPool{
		{Alias: "  pool-1  ", Models: []CrossProviderPoolModel{{Provider: " p1 ", Model: " m1 "}}},
		{Alias: "", Models: []CrossProviderPoolModel{{Provider: "p2", Model: "m2"}}},
		{Alias: "pool-1", Models: []CrossProviderPoolModel{{Provider: "p3", Model: "m3"}}},
		{Alias: "pool-2", Models: []CrossProviderPoolModel{{Provider: "", Model: "m4"}}},
		{Alias: "pool-3", Models: []CrossProviderPoolModel{{Provider: "p5", Model: ""}}},
	}

	result := SanitizePools(pools)

	assert.Len(t, result, 1)
	assert.Equal(t, "pool-1", result[0].Alias)
	assert.Equal(t, "p1", result[0].Models[0].Provider)
	assert.Equal(t, "m1", result[0].Models[0].Model)
}

func TestSanitizePools_Empty(t *testing.T) {
	assert.Nil(t, SanitizePools(nil))
	assert.Empty(t, SanitizePools([]CrossProviderPool{}))
}
