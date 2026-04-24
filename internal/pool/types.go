// Package pool provides cross-provider pool management, decoupled from the
// model registry. A cross-provider pool maps a single alias to per-provider
// model names, allowing one logical model name to route to different actual
// models depending on the selected provider.
package pool

// CrossProviderPool defines a virtual model alias that routes to multiple providers.
type CrossProviderPool struct {
	Alias  string                   `yaml:"alias" json:"alias"`
	Models []CrossProviderPoolModel `yaml:"models" json:"models"`
}

// CrossProviderPoolModel maps a provider to its actual model name within a pool.
type CrossProviderPoolModel struct {
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model" json:"model"`
}
