package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"

	log "github.com/sirupsen/logrus"
)

const (
	// defaultBaseURL matches the opencode-go provider in config.yaml.
	defaultBaseURL = "https://opencode.ai/zen/go/v1"
	// defaultAPIKey matches the opencode-go provider key in config.yaml.
	defaultAPIKey = "sk-xzEKhRzBOhqfiIA8mrYm1R0cfgs2GYam6t0SkHz0NtnssUnPek1EeAKz6NFfuwpw"
)

// pluginConfig is the plugin-owned configuration parsed from the host-provided
// config_yaml (plugins.configs.opencode-responses in config.yaml).
type pluginConfig struct {
	Enabled bool     `yaml:"enabled"`
	BaseURL string   `yaml:"base-url"`
	APIKey  string   `yaml:"api-key"`
	Models  []string `yaml:"models"`
}

// configStore holds the latest parsed config; model.route and executor calls
// read it on every invocation.
var configStore atomic.Value

// defaultConfig matches the host default: plugins are disabled unless the
// host injects enabled: true (see internal/pluginhost/config.go). The
// remaining fields carry the opencode-go provider defaults so that setting
// only `enabled: true` is enough to activate the fix.
func defaultConfig() pluginConfig {
	return pluginConfig{
		Enabled: false,
		BaseURL: defaultBaseURL,
		APIKey:  defaultAPIKey,
		Models:  []string{"muse-spark-1.2-contributor"},
	}
}

func currentConfig() pluginConfig {
	if v, ok := configStore.Load().(pluginConfig); ok {
		return v
	}
	return defaultConfig()
}

// lifecycleRequest mirrors the host-side rpcLifecycleRequest: the host sends
// config_yaml as a JSON object whose []byte field is base64-encoded YAML
// (see internal/pluginhost/rpc_schema.go + rpc_client.go).
type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

// applyConfig parses the host-provided config_yaml and replaces the stored
// config. On parse failure the previous config is kept.
func applyConfig(request []byte) {
	if len(request) > 0 {
		var lr lifecycleRequest
		if err := json.Unmarshal(request, &lr); err == nil && len(lr.ConfigYAML) > 0 {
			cfg := parsePluginConfig(lr.ConfigYAML)
			configStore.Store(cfg)
			log.Infof("opencode-responses: config applied (enabled=%v base-url=%q models=%v)", cfg.Enabled, cfg.BaseURL, cfg.Models)
			return
		}
	}
	// Empty or opaque payload: apply defaults.
	configStore.Store(defaultConfig())
}

func parsePluginConfig(raw []byte) pluginConfig {
	cfg := defaultConfig()
	if len(raw) == 0 {
		return cfg
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		log.Warnf("opencode-responses: invalid config_yaml, keeping previous config: %v", err)
		return currentConfig()
	}
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	return cfg
}
