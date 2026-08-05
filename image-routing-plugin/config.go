package main

import (
	"encoding/json"
	"sync/atomic"

	"gopkg.in/yaml.v3"

	log "github.com/sirupsen/logrus"
)

type routingConfig struct {
	Enabled          bool     `yaml:"enabled"`
	Fallback         string   `yaml:"fallback"`
	FallbackProvider string   `yaml:"fallback-provider"`
	Models           []string `yaml:"models"`
}

// configStore holds the latest parsed config; model.route reads it on every call.
var configStore atomic.Value

// defaultConfig matches the host default: plugins are disabled unless the
// host injects enabled: true (see internal/pluginhost/config.go).
func defaultConfig() routingConfig {
	return routingConfig{Enabled: false}
}

func currentConfig() routingConfig {
	if v, ok := configStore.Load().(routingConfig); ok {
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

// applyConfig parses the host-provided config_yaml and replaces the stored config.
// On parse failure the previous config is kept.
func applyConfig(request []byte) {
	if len(request) > 0 {
		var lr lifecycleRequest
		if err := json.Unmarshal(request, &lr); err == nil && len(lr.ConfigYAML) > 0 {
			cfg := parseRoutingConfig(lr.ConfigYAML)
			configStore.Store(cfg)
			log.Infof("image-routing: config applied (enabled=%v fallback=%q fallback-provider=%q models=%v)", cfg.Enabled, cfg.Fallback, cfg.FallbackProvider, cfg.Models)
			return
		}
	}
	// Empty or opaque payload: apply defaults.
	configStore.Store(defaultConfig())
}

func parseRoutingConfig(raw []byte) routingConfig {
	cfg := defaultConfig()
	if len(raw) == 0 {
		return cfg
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		log.Warnf("image-routing: invalid config_yaml, keeping previous config: %v", err)
		return currentConfig()
	}
	return cfg
}
