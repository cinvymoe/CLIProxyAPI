package pool

// PoolResolver centralizes cross-provider pool resolution logic. Consumers
// call ResolveModel instead of repeating the IsPoolAlias + GetPoolModel
// two-step themselves.
type PoolResolver interface {
	// IsPool reports whether modelName is a registered pool alias.
	IsPool(modelName string) bool
	// ResolveModel returns the provider-specific model name for the given
	// pool alias. If modelName is not a pool alias, it returns ("", false).
	ResolveModel(modelName, provider string) (resolvedModel string, isPool bool)
	// PoolProviders returns the list of providers registered under the
	// given pool alias, or nil if the alias is unknown.
	PoolProviders(modelName string) []string
}

type defaultPoolResolver struct {
	mgr *Manager
}

// NewResolver returns a PoolResolver backed by the given Manager.
func NewResolver(mgr *Manager) PoolResolver {
	return &defaultPoolResolver{mgr: mgr}
}

func (r *defaultPoolResolver) IsPool(modelName string) bool {
	return r.mgr.IsPoolAlias(modelName)
}

func (r *defaultPoolResolver) ResolveModel(modelName, provider string) (string, bool) {
	if !r.mgr.IsPoolAlias(modelName) {
		return "", false
	}
	return r.mgr.GetPoolModel(modelName, provider), true
}

func (r *defaultPoolResolver) PoolProviders(modelName string) []string {
	return r.mgr.GetPoolProviders(modelName)
}
