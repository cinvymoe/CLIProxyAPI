package main

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func TestParsePluginConfig_FullConfig(t *testing.T) {
	raw := []byte("enabled: true\nbase-url: https://example.com/v2\napi-key: sk-test\nmodels:\n  - mimo-v2.5\n  - muse-spark-1.2-contributor\n")
	cfg := parsePluginConfig(raw)
	if !cfg.Enabled || cfg.BaseURL != "https://example.com/v2" || cfg.APIKey != "sk-test" {
		t.Fatalf("cfg = %+v, want enabled+custom base-url+api-key", cfg)
	}
	if len(cfg.Models) != 2 || cfg.Models[0] != "mimo-v2.5" {
		t.Fatalf("models = %v, want [mimo-v2.5 muse-spark-1.2-contributor]", cfg.Models)
	}
}

func TestParsePluginConfig_EnabledOnlyKeepsDefaults(t *testing.T) {
	cfg := parsePluginConfig([]byte("enabled: true\npriority: 0\n"))
	if !cfg.Enabled {
		t.Fatal("enabled = false, want true")
	}
	if cfg.BaseURL != defaultBaseURL || cfg.APIKey != defaultAPIKey {
		t.Fatalf("cfg = %+v, want default base-url and api-key", cfg)
	}
	if len(cfg.Models) != 1 || cfg.Models[0] != "muse-spark-1.2-contributor" {
		t.Fatalf("models = %v, want [muse-spark-1.2-contributor]", cfg.Models)
	}
}

func TestParsePluginConfig_EnabledMissingDefaultsFalse(t *testing.T) {
	cfg := parsePluginConfig([]byte("base-url: https://example.com/v1"))
	if cfg.Enabled {
		t.Fatal("enabled = true, want false when the host omits enabled")
	}
	if cfg.BaseURL != "https://example.com/v1" {
		t.Fatalf("base-url = %q, want https://example.com/v1", cfg.BaseURL)
	}
}

func TestParsePluginConfig_EmptyBaseURLRestoresDefault(t *testing.T) {
	cfg := parsePluginConfig([]byte("enabled: true\nbase-url: \"  \"\n"))
	if cfg.BaseURL != defaultBaseURL {
		t.Fatalf("base-url = %q, want default %q", cfg.BaseURL, defaultBaseURL)
	}
}

func TestParsePluginConfig_InvalidYAMLKeepsPrevious(t *testing.T) {
	configStore.Store(pluginConfig{Enabled: true, BaseURL: "https://keep-me.example", APIKey: "sk-keep", Models: []string{"m"}})
	cfg := parsePluginConfig([]byte("::: not yaml :::"))
	if cfg.BaseURL != "https://keep-me.example" {
		t.Fatalf("base-url = %q, want previous config kept", cfg.BaseURL)
	}
}

func TestApplyConfig_Base64Envelope(t *testing.T) {
	yamlRaw := []byte("enabled: true\nmodels:\n  - mimo-v2.5\n")
	envelope := fmt.Sprintf(`{"config_yaml":%q}`, base64.StdEncoding.EncodeToString(yamlRaw))
	applyConfig([]byte(envelope))
	cfg := currentConfig()
	if !cfg.Enabled || len(cfg.Models) != 1 || cfg.Models[0] != "mimo-v2.5" {
		t.Fatalf("cfg = %+v, want enabled with models [mimo-v2.5]", cfg)
	}
	if cfg.BaseURL != defaultBaseURL {
		t.Fatalf("base-url = %q, want default %q", cfg.BaseURL, defaultBaseURL)
	}
}

func TestApplyConfig_EmptyPayloadAppliesDefaults(t *testing.T) {
	configStore.Store(pluginConfig{Enabled: true, BaseURL: "https://stale.example", APIKey: "sk-stale", Models: []string{"stale"}})
	applyConfig(nil)
	cfg := currentConfig()
	if cfg.Enabled || cfg.BaseURL != defaultBaseURL || cfg.APIKey != defaultAPIKey {
		t.Fatalf("cfg = %+v, want defaults", cfg)
	}
}
