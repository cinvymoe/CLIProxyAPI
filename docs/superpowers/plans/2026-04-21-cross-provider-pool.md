# Cross-Provider Model Pool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable configuring a virtual model alias that routes requests to multiple providers with round-robin load balancing, where each provider can use a different upstream model name.

**Architecture:** Add a `cross-provider-pool` config section that maps aliases to multiple (provider, model) pairs. At routing time, detect pool aliases and return all pool providers. The existing `pickMixed()` scheduler handles round-robin. Before execution, rewrite the model name per-provider.

**Tech Stack:** Go 1.26+, existing registry and conductor infrastructure

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/config/config.go` | Config types for CrossProviderPool |
| `internal/config/sanitize.go` | Validation for pool config |
| `internal/registry/model_registry.go` | Pool storage and lookup methods |
| `internal/registry/cross_provider_pool.go` | Pool-specific logic (new file) |
| `sdk/api/handlers/handlers.go` | Detect pool, return multi-provider |
| `sdk/cliproxy/auth/conductor.go` | Model name rewriting before execution |
| `config.example.yaml` | Example pool configuration |

---

### Task 1: Add Config Types

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add CrossProviderPool structs to config.go**

Add after line 614 (after `OpenAICompatibilityModel`):

```go
// CrossProviderPool defines a virtual model alias that routes to multiple providers
// with round-robin load balancing. Each provider in the pool can use a different
// upstream model name.
type CrossProviderPool struct {
	// Alias is the client-visible model name for this pool.
	Alias string `yaml:"alias" json:"alias"`

	// Models lists the provider-specific models to round-robin across.
	Models []CrossProviderPoolModel `yaml:"models" json:"models"`
}

// CrossProviderPoolModel represents a single provider+model mapping in a pool.
type CrossProviderPoolModel struct {
	// Provider is the openai-compatibility provider name.
	Provider string `yaml:"provider" json:"provider"`

	// Model is the upstream model name for this provider.
	Model string `yaml:"model" json:"model"`
}
```

- [ ] **Step 2: Add CrossProviderPool field to Config struct**

Add after line 117 (after `OpenAICompatibility` field):

```go
	// CrossProviderPool defines virtual model aliases that route to multiple providers.
	// Requests to a pool alias will round-robin across the configured providers.
	CrossProviderPool []CrossProviderPool `yaml:"cross-provider-pool,omitempty" json:"cross-provider-pool,omitempty"`
```

- [ ] **Step 3: Verify compilation**

Run: `go build -o test-output ./cmd/server && rm test-output`
Expected: No errors

---

### Task 2: Add Config Validation

**Files:**
- Modify: `internal/config/sanitize.go`

- [ ] **Step 1: Find the SanitizeConfig function**

Read `internal/config/sanitize.go` to understand the existing validation pattern.

- [ ] **Step 2: Add SanitizeCrossProviderPool function**

Add a new function after the existing sanitize functions:

```go
// SanitizeCrossProviderPool validates cross-provider pool configuration.
// It checks for empty aliases, missing models, and empty provider/model fields.
func SanitizeCrossProviderPool(pools []config.CrossProviderPool) error {
	if len(pools) == 0 {
		return nil
	}

	seenAliases := make(map[string]bool, len(pools))

	for i, pool := range pools {
		// Check alias
		if pool.Alias == "" {
			return fmt.Errorf("cross-provider-pool[%d]: alias cannot be empty", i)
		}

		// Check for duplicate aliases
		if seenAliases[pool.Alias] {
			return fmt.Errorf("cross-provider-pool[%d]: duplicate alias %q", i, pool.Alias)
		}
		seenAliases[pool.Alias] = true

		// Check models list
		if len(pool.Models) == 0 {
			return fmt.Errorf("cross-provider-pool[%d]: alias %q has no models", i, pool.Alias)
		}

		// Check each model entry
		for j, m := range pool.Models {
			if m.Provider == "" {
				return fmt.Errorf("cross-provider-pool[%d].models[%d]: provider cannot be empty", i, j)
			}
			if m.Model == "" {
				return fmt.Errorf("cross-provider-pool[%d].models[%d]: model cannot be empty", i, j)
			}
		}
	}

	return nil
}
```

- [ ] **Step 3: Call SanitizeCrossProviderPool from SanitizeConfig**

Find where other sanitize functions are called in `SanitizeConfig()` and add:

```go
	if err := SanitizeCrossProviderPool(cfg.CrossProviderPool); err != nil {
		return err
	}
```

- [ ] **Step 4: Verify compilation**

Run: `go build -o test-output ./cmd/server && rm test-output`
Expected: No errors

---

### Task 3: Add Pool Registry Storage and Lookup

**Files:**
- Create: `internal/registry/cross_provider_pool.go`
- Modify: `internal/registry/model_registry.go`

- [ ] **Step 1: Create cross_provider_pool.go with pool management logic**

Create file `internal/registry/cross_provider_pool.go`:

```go
package registry

import (
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// CrossProviderPoolManager manages cross-provider pool configurations.
// It provides thread-safe storage and lookup for pool aliases.
type CrossProviderPoolManager struct {
	mu    sync.RWMutex
	pools map[string]*config.CrossProviderPool // alias -> pool
}

// NewCrossProviderPoolManager creates a new pool manager.
func NewCrossProviderPoolManager() *CrossProviderPoolManager {
	return &CrossProviderPoolManager{
		pools: make(map[string]*config.CrossProviderPool),
	}
}

// RegisterPool registers a cross-provider pool.
// If a pool with the same alias exists, it is replaced.
func (m *CrossProviderPoolManager) RegisterPool(pool config.CrossProviderPool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools[pool.Alias] = &pool
}

// UnregisterPool removes a pool by alias.
func (m *CrossProviderPoolManager) UnregisterPool(alias string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pools, alias)
}

// Clear removes all pools.
func (m *CrossProviderPoolManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pools = make(map[string]*config.CrossProviderPool)
}

// GetPool returns the pool for an alias, or nil if not found.
func (m *CrossProviderPoolManager) GetPool(alias string) *config.CrossProviderPool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pools[alias]
}

// IsPoolAlias returns true if the given model name is a pool alias.
func (m *CrossProviderPoolManager) IsPoolAlias(modelName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.pools[modelName]
	return exists
}

// GetPoolProviders returns the list of provider names for a pool alias.
// Returns nil if the alias is not a pool.
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

// GetPoolModel returns the upstream model name for a specific provider in a pool.
// Returns empty string if the alias is not a pool or provider is not in the pool.
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

// RegisterPools registers multiple pools at once, clearing any existing pools.
func (m *CrossProviderPoolManager) RegisterPools(pools []config.CrossProviderPool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pools = make(map[string]*config.CrossProviderPool, len(pools))
	for i := range pools {
		m.pools[pools[i].Alias] = &pools[i]
	}
}

// GetAllPoolAliases returns all registered pool aliases.
func (m *CrossProviderPoolManager) GetAllPoolAliases() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	aliases := make([]string, 0, len(m.pools))
	for alias := range m.pools {
		aliases = append(aliases, alias)
	}
	return aliases
}
```

- [ ] **Step 2: Add pool manager to ModelRegistry struct**

Find the `ModelRegistry` struct in `internal/registry/model_registry.go` and add a field:

```go
	// crossProviderPoolManager manages cross-provider pool aliases.
	crossProviderPoolManager *CrossProviderPoolManager
```

- [ ] **Step 3: Initialize pool manager in NewModelRegistry**

Find `NewModelRegistry()` function and add initialization:

```go
		crossProviderPoolManager: NewCrossProviderPoolManager(),
```

- [ ] **Step 4: Add accessor methods to ModelRegistry**

Add these methods to `ModelRegistry`:

```go
// GetCrossProviderPoolManager returns the cross-provider pool manager.
func (r *ModelRegistry) GetCrossProviderPoolManager() *CrossProviderPoolManager {
	return r.crossProviderPoolManager
}

// IsCrossProviderPoolAlias returns true if the model name is a pool alias.
func (r *ModelRegistry) IsCrossProviderPoolAlias(modelName string) bool {
	return r.crossProviderPoolManager.IsPoolAlias(modelName)
}

// GetCrossProviderPool returns the pool for an alias, or nil if not a pool.
func (r *ModelRegistry) GetCrossProviderPool(alias string) *config.CrossProviderPool {
	return r.crossProviderPoolManager.GetPool(alias)
}

// GetCrossProviderPoolProviders returns providers for a pool alias.
func (r *ModelRegistry) GetCrossProviderPoolProviders(alias string) []string {
	return r.crossProviderPoolManager.GetPoolProviders(alias)
}

// GetCrossProviderPoolModel returns the upstream model for a provider in a pool.
func (r *ModelRegistry) GetCrossProviderPoolModel(alias, provider string) string {
	return r.crossProviderPoolManager.GetPoolModel(alias, provider)
}

// RegisterCrossProviderPools registers all cross-provider pools from config.
func (r *ModelRegistry) RegisterCrossProviderPools(pools []config.CrossProviderPool) {
	r.crossProviderPoolManager.RegisterPools(pools)
}
```

- [ ] **Step 5: Verify compilation**

Run: `go build -o test-output ./cmd/server && rm test-output`
Expected: No errors

---

### Task 4: Wire Pool Registration into Startup

**Files:**
- Modify: `cmd/server/main.go` or the appropriate initialization location

- [ ] **Step 1: Find where config is loaded and registry is initialized**

Search for where `GetGlobalRegistry()` is used and config is loaded.

- [ ] **Step 2: Add pool registration after config load**

Find the location where config is loaded (likely in `cmd/server/main.go` or a setup function). Add after config validation:

```go
	// Register cross-provider pools
	registry.GetGlobalRegistry().RegisterCrossProviderPools(cfg.CrossProviderPool)
```

- [ ] **Step 3: Verify compilation**

Run: `go build -o test-output ./cmd/server && rm test-output`
Expected: No errors

---

### Task 5: Modify Request Routing to Detect Pool

**Files:**
- Modify: `sdk/api/handlers/handlers.go`

- [ ] **Step 1: Find getRequestDetails function**

Locate `getRequestDetails()` function in `sdk/api/handlers/handlers.go` (around line 781 based on earlier exploration).

- [ ] **Step 2: Add pool detection logic**

After the existing provider resolution logic (where `GetProviderName()` is called), add pool detection:

```go
	// Check if this is a cross-provider pool alias
	if registry.GetGlobalRegistry().IsCrossProviderPoolAlias(modelName) {
		poolProviders := registry.GetGlobalRegistry().GetCrossProviderPoolProviders(modelName)
		if len(poolProviders) > 0 {
			// Use pool providers instead of registry-resolved providers
			providers = poolProviders
			// Mark that this is a pool request for later model rewriting
			details.IsCrossProviderPool = true
		}
	}
```

- [ ] **Step 3: Add IsCrossProviderPool field to request details struct**

Find the struct that `getRequestDetails()` returns and add:

```go
	// IsCrossProviderPool indicates this request is for a cross-provider pool alias.
	IsCrossProviderPool bool
```

- [ ] **Step 4: Verify compilation**

Run: `go build -o test-output ./cmd/server && rm test-output`
Expected: No errors

---

### Task 6: Add Model Rewriting Before Execution

**Files:**
- Modify: `sdk/cliproxy/auth/conductor.go`

- [ ] **Step 1: Find where model name is used for execution**

Locate the execution path in `conductor.go` - likely in `executeMixedOnce()` or similar function where the actual API call is prepared.

- [ ] **Step 2: Add model rewriting logic**

Before the executor is called, add logic to rewrite the model name if this is a cross-provider pool:

```go
	// Rewrite model name for cross-provider pool if needed
	if registry.GetGlobalRegistry().IsCrossProviderPoolAlias(req.Model) {
		upstreamModel := registry.GetGlobalRegistry().GetCrossProviderPoolModel(req.Model, providerName)
		if upstreamModel != "" && upstreamModel != req.Model {
			// Preserve thinking suffix if present
			originalModel := req.Model
			req.Model = upstreamModel
			// Log the rewrite for debugging
			log.Debugf("Cross-provider pool: rewrote model %q to %q for provider %q", originalModel, upstreamModel, providerName)
		}
	}
```

- [ ] **Step 3: Handle thinking suffix preservation**

If the codebase has a `preserveRequestedModelSuffix()` function or similar, ensure it's called after the model rewrite. Check `internal/thinking/` for suffix handling.

- [ ] **Step 4: Verify compilation**

Run: `go build -o test-output ./cmd/server && rm test-output`
Expected: No errors

---

### Task 7: Add Example Configuration

**Files:**
- Modify: `config.example.yaml`

- [ ] **Step 1: Add cross-provider-pool section to config.example.yaml**

Add after the existing configuration sections (after `xunfei-retry` or similar):

```yaml
# Cross-provider pool configuration
# Define virtual model aliases that route to multiple providers with round-robin.
# Each provider in the pool can use a different upstream model name.
#
# cross-provider-pool:
#   - alias: "smart-model"
#     models:
#       - provider: "openai-compat-provider-1"
#         model: "gpt-4"
#       - provider: "openai-compat-provider-2"
#         model: "claude-3-opus"
#
# Requests to "smart-model" will round-robin between the two providers,
# using "gpt-4" for the first and "claude-3-opus" for the second.
```

- [ ] **Step 2: Verify YAML syntax**

Run: `go run ./cmd/server --config config.example.yaml --help 2>&1 | head -5`
Expected: No YAML parsing errors

---

### Task 8: Write Unit Tests

**Files:**
- Create: `internal/registry/cross_provider_pool_test.go`

- [ ] **Step 1: Create test file**

Create `internal/registry/cross_provider_pool_test.go`:

```go
package registry

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestCrossProviderPoolManager_RegisterPool(t *testing.T) {
	m := NewCrossProviderPoolManager()

	pool := config.CrossProviderPool{
		Alias: "test-model",
		Models: []config.CrossProviderPoolModel{
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

	pool := config.CrossProviderPool{
		Alias: "test-model",
		Models: []config.CrossProviderPoolModel{
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

	pool := config.CrossProviderPool{
		Alias: "test-model",
		Models: []config.CrossProviderPoolModel{
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

	pools := []config.CrossProviderPool{
		{
			Alias: "model-1",
			Models: []config.CrossProviderPoolModel{
				{Provider: "p1", Model: "m1"},
			},
		},
		{
			Alias: "model-2",
			Models: []config.CrossProviderPoolModel{
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

	pool := config.CrossProviderPool{
		Alias: "test-model",
		Models: []config.CrossProviderPoolModel{
			{Provider: "provider1", Model: "model-a"},
		},
	}

	m.RegisterPool(pool)
	assert.True(t, m.IsPoolAlias("test-model"))

	m.Clear()
	assert.False(t, m.IsPoolAlias("test-model"))
}
```

- [ ] **Step 2: Run tests**

Run: `go test -v ./internal/registry/ -run TestCrossProviderPool`
Expected: All tests pass

---

### Task 9: Write Config Validation Tests

**Files:**
- Modify: `internal/config/sanitize_test.go` (or create if needed)

- [ ] **Step 1: Add tests for SanitizeCrossProviderPool**

```go
func TestSanitizeCrossProviderPool(t *testing.T) {
	tests := []struct {
		name    string
		pools   []config.CrossProviderPool
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty pools",
			pools:   nil,
			wantErr: false,
		},
		{
			name: "valid pool",
			pools: []config.CrossProviderPool{
				{
					Alias: "test-model",
					Models: []config.CrossProviderPoolModel{
						{Provider: "p1", Model: "m1"},
						{Provider: "p2", Model: "m2"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty alias",
			pools: []config.CrossProviderPool{
				{
					Alias: "",
					Models: []config.CrossProviderPoolModel{
						{Provider: "p1", Model: "m1"},
					},
				},
			},
			wantErr: true,
			errMsg:  "alias cannot be empty",
		},
		{
			name: "duplicate alias",
			pools: []config.CrossProviderPool{
				{
					Alias: "test",
					Models: []config.CrossProviderPoolModel{
						{Provider: "p1", Model: "m1"},
					},
				},
				{
					Alias: "test",
					Models: []config.CrossProviderPoolModel{
						{Provider: "p2", Model: "m2"},
					},
				},
			},
			wantErr: true,
			errMsg:  "duplicate alias",
		},
		{
			name: "empty models",
			pools: []config.CrossProviderPool{
				{
					Alias:  "test",
					Models: nil,
				},
			},
			wantErr: true,
			errMsg:  "has no models",
		},
		{
			name: "empty provider",
			pools: []config.CrossProviderPool{
				{
					Alias: "test",
					Models: []config.CrossProviderPoolModel{
						{Provider: "", Model: "m1"},
					},
				},
			},
			wantErr: true,
			errMsg:  "provider cannot be empty",
		},
		{
			name: "empty model",
			pools: []config.CrossProviderPool{
				{
					Alias: "test",
					Models: []config.CrossProviderPoolModel{
						{Provider: "p1", Model: ""},
					},
				},
			},
			wantErr: true,
			errMsg:  "model cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SanitizeCrossProviderPool(tt.pools)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test -v ./internal/config/ -run TestSanitizeCrossProviderPool`
Expected: All tests pass

---

### Task 10: Integration Test

**Files:**
- Create: `test/cross_provider_pool_test.go`

- [ ] **Step 1: Create integration test**

Create a test that verifies the full flow from config to routing:

```go
// +build integration

package test

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
)

func TestCrossProviderPoolIntegration(t *testing.T) {
	// Setup
	reg := registry.NewModelRegistry()

	pools := []config.CrossProviderPool{
		{
			Alias: "smart-model",
			Models: []config.CrossProviderPoolModel{
				{Provider: "provider1", Model: "gpt-4"},
				{Provider: "provider2", Model: "claude-3"},
			},
		},
	}

	reg.RegisterCrossProviderPools(pools)

	// Test pool detection
	assert.True(t, reg.IsCrossProviderPoolAlias("smart-model"))
	assert.False(t, reg.IsCrossProviderPoolAlias("other-model"))

	// Test provider list
	providers := reg.GetCrossProviderPoolProviders("smart-model")
	assert.Equal(t, []string{"provider1", "provider2"}, providers)

	// Test model rewriting
	assert.Equal(t, "gpt-4", reg.GetCrossProviderPoolModel("smart-model", "provider1"))
	assert.Equal(t, "claude-3", reg.GetCrossProviderPoolModel("smart-model", "provider2"))
}
```

- [ ] **Step 2: Run integration test**

Run: `go test -v ./test/ -run TestCrossProviderPoolIntegration -tags=integration`
Expected: Test passes

---

### Task 11: Final Verification

- [ ] **Step 1: Run all tests**

Run: `go test ./...`
Expected: All tests pass

- [ ] **Step 2: Build the binary**

Run: `go build -o cli-proxy-api ./cmd/server`
Expected: Build succeeds

- [ ] **Step 3: Format code**

Run: `gofmt -w .`
Expected: No output (files formatted)

- [ ] **Step 4: Run linter (if available)**

Run: `go vet ./...`
Expected: No issues

---

## Summary

This implementation adds cross-provider model pool support with:

1. **Config types** - `CrossProviderPool` and `CrossProviderPoolModel` structs
2. **Validation** - `SanitizeCrossProviderPool()` ensures valid config
3. **Registry storage** - `CrossProviderPoolManager` for thread-safe pool lookup
4. **Routing** - Pool detection in `getRequestDetails()` returns all pool providers
5. **Model rewriting** - Per-provider model name translation before execution
6. **Tests** - Unit and integration tests for all components

The existing `pickMixed()` scheduler handles the round-robin logic across providers, so no scheduler changes are needed.
