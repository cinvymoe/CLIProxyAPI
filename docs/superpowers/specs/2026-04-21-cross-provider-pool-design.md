# Cross-Provider Model Pool Design

## Overview

Enable configuring a virtual model alias that routes requests to multiple providers with round-robin load balancing. Each provider in the pool can use a different upstream model name.

## User Story

As a user, I want to configure a model alias `astron-code-latest` that round-robins between:
- `xfyun-coding` provider's `astron-code-latest` model
- `cucloud` provider's `glm-5` model

So that I can distribute load across multiple providers with different model capabilities.

## Configuration Schema

```yaml
cross-provider-pool:
  - alias: "astron-code-latest"
    models:
      - provider: "xfyun-coding"
        model: "astron-code-latest"
      - provider: "cucloud"
        model: "glm-5"
  - alias: "smart-model"
    models:
      - provider: "minimax-cn"
        model: "MiniMax-M2.7-highspeed"
      - provider: "cucloud"
        model: "DeepSeek-V3.1"
```

### Config Structure

```go
// CrossProviderPool defines a virtual model alias that routes to multiple providers.
type CrossProviderPool struct {
    // Alias is the client-visible model name.
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

## Architecture

### Request Flow

```
1. Client requests model "astron-code-latest"
2. getRequestDetails() checks cross-provider-pool registry
3. If match found:
   a. Return all pool providers: ["xfyun-coding", "cucloud"]
   b. Store pool mapping in request context
4. pickMixed() selects a provider via round-robin (existing logic)
5. Before execution, rewrite model name:
   - If provider="cucloud", model="astron-code-latest" → "glm-5"
   - If provider="xfyun-coding", model stays "astron-code-latest"
6. Execute request with rewritten model name
```

### Key Components

| Component | File | Change |
|-----------|------|--------|
| Config type | `internal/config/config.go` | Add `CrossProviderPool` struct and field |
| Pool registry | `internal/registry/model_registry.go` | Register pool aliases, add lookup methods |
| Request routing | `sdk/api/handlers/handlers.go` | Detect pool alias, return multi-provider list |
| Model rewriting | `sdk/cliproxy/auth/conductor.go` | Rewrite model name per-provider before execution |
| Config validation | `internal/config/config.go` | Add `SanitizeCrossProviderPool()` |

## Implementation Details

### 1. Config Structure (internal/config/config.go)

Add to `Config` struct:
```go
// CrossProviderPool defines virtual model aliases that route to multiple providers.
CrossProviderPool []CrossProviderPool `yaml:"cross-provider-pool,omitempty" json:"cross-provider-pool,omitempty"`
```

Add new structs:
```go
type CrossProviderPool struct {
    Alias  string                    `yaml:"alias" json:"alias"`
    Models []CrossProviderPoolModel  `yaml:"models" json:"models"`
}

type CrossProviderPoolModel struct {
    Provider string `yaml:"provider" json:"provider"`
    Model    string `yaml:"model" json:"model"`
}
```

### 2. Pool Registry (internal/registry/model_registry.go)

Add methods to `ModelRegistry`:
```go
// RegisterCrossProviderPool registers a pool alias as available from all pool providers.
func (r *ModelRegistry) RegisterCrossProviderPool(pool CrossProviderPool) error

// GetCrossProviderPool returns the pool config for an alias, or nil if not a pool.
func (r *ModelRegistry) GetCrossProviderPool(alias string) *CrossProviderPool

// GetCrossProviderModel returns the upstream model name for a provider in a pool.
func (r *ModelRegistry) GetCrossProviderModel(alias, provider string) string
```

Store pools in a new field:
```go
type ModelRegistry struct {
    // ... existing fields ...
    crossProviderPools map[string]*CrossProviderPool  // alias -> pool
}
```

### 3. Request Routing (sdk/api/handlers/handlers.go)

In `getRequestDetails()`:
```go
// After resolving providers via GetProviderName(), check for pool
if pool := registry.GetGlobalRegistry().GetCrossProviderPool(modelName); pool != nil {
    // Use pool providers instead of registry providers
    providers = pool.GetProviders()
    // Store pool in context for later model rewriting
    ctx = context.WithValue(ctx, crossProviderPoolKey, pool)
}
```

### 4. Model Rewriting (sdk/cliproxy/auth/conductor.go)

In `executeMixedOnce()` or before calling the executor:
```go
// Check if this is a cross-provider pool request
if pool := registry.GetGlobalRegistry().GetCrossProviderPool(req.Model); pool != nil {
    // Rewrite model name for this provider
    upstreamModel := registry.GetGlobalRegistry().GetCrossProviderModel(req.Model, providerName)
    if upstreamModel != "" && upstreamModel != req.Model {
        req.Model = upstreamModel
    }
}
```

### 5. Config Validation (internal/config/config.go)

Add `SanitizeCrossProviderPool()`:
```go
func SanitizeCrossProviderPool(pools []CrossProviderPool, providers []string) error {
    for _, pool := range pools {
        if pool.Alias == "" {
            return errors.New("cross-provider-pool: alias cannot be empty")
        }
        if len(pool.Models) == 0 {
            return fmt.Errorf("cross-provider-pool: %s has no models", pool.Alias)
        }
        for _, m := range pool.Models {
            if m.Provider == "" || m.Model == "" {
                return fmt.Errorf("cross-provider-pool: %s has empty provider or model", pool.Alias)
            }
            // Warn if provider not found (but don't error - provider may be added later)
        }
    }
    return nil
}
```

## Edge Cases

### Provider Unavailable
If a pool provider has no available auths (all in cooldown or not configured), `pickMixed()` automatically skips it and tries the next provider. No special handling needed.

### Thinking Suffixes
Pool aliases preserve thinking suffixes. The existing `thinking.ParseSuffix()` and `preserveRequestedModelSuffix()` pipeline handles this. When rewriting `astron-code-latest(8192)` → `glm-5`, the suffix is preserved: `glm-5(8192)`.

### Model Listing
Pool aliases appear in `/v1/models` responses. The model info (owned_by, context_length) is taken from the first provider in the pool.

### Pool Alias Conflicts
If a pool alias matches an existing model name, the pool takes precedence. This is documented as explicit config wins over implicit registration.

### Single-Provider Pool
A pool with one provider degenerates to normal single-provider routing. `pickMixed()` optimizes this to `pickSingle()`.

## Testing

1. **Config parsing**: Unit test for YAML unmarshaling of `cross-provider-pool`
2. **Pool registration**: Unit test for `RegisterCrossProviderPool()` and lookup methods
3. **Routing**: Integration test verifying requests route to all pool providers
4. **Round-robin**: Test that requests distribute across providers
5. **Model rewriting**: Test that upstream model name is correct per-provider
6. **Fallback**: Test that unavailable providers are skipped

## Files Changed

| File | Changes |
|------|---------|
| `internal/config/config.go` | Add config structs, validation |
| `internal/registry/model_registry.go` | Add pool storage and lookup methods |
| `sdk/api/handlers/handlers.go` | Detect pool, return multi-provider |
| `sdk/cliproxy/auth/conductor.go` | Rewrite model name before execution |
| `config.example.yaml` | Add example pool configuration |

## Effort Estimate

Medium: ~1-2 days of work
