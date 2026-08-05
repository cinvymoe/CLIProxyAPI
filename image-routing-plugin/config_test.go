package main

import "testing"

func TestParseRoutingConfig_FullConfig(t *testing.T) {
	raw := []byte("enabled: true\nfallback: mimo-v2.5\nfallback-provider: opencode-go\nmodels:\n  - deepseek-v4-flash\n")
	cfg := parseRoutingConfig(raw)
	if !cfg.Enabled || cfg.Fallback != "mimo-v2.5" || cfg.FallbackProvider != "opencode-go" {
		t.Fatalf("cfg = %+v, want enabled+fallback+mimo-v2.5+opencode-go", cfg)
	}
	if len(cfg.Models) != 1 || cfg.Models[0] != "deepseek-v4-flash" {
		t.Fatalf("models = %v, want [deepseek-v4-flash]", cfg.Models)
	}
}

func TestParseRoutingConfig_MissingFields(t *testing.T) {
	cfg := parseRoutingConfig([]byte("enabled: true"))
	if !cfg.Enabled || cfg.Fallback != "" || cfg.FallbackProvider != "" || len(cfg.Models) != 0 {
		t.Fatalf("cfg = %+v, want only enabled=true", cfg)
	}
}

func TestParseRoutingConfig_EnabledMissingDefaultsFalse(t *testing.T) {
	cfg := parseRoutingConfig([]byte("fallback: mimo-v2.5"))
	if cfg.Enabled {
		t.Fatal("enabled = true, want false when the host omits enabled")
	}
	if cfg.Fallback != "mimo-v2.5" {
		t.Fatalf("fallback = %q, want mimo-v2.5", cfg.Fallback)
	}
}

func TestParseRoutingConfig_InvalidYAMLKeepsPrevious(t *testing.T) {
	configStore.Store(routingConfig{Enabled: true, Fallback: "keep-me", FallbackProvider: "p", Models: []string{"m"}})
	cfg := parseRoutingConfig([]byte("::: not yaml :::"))
	if cfg.Fallback != "keep-me" {
		t.Fatalf("fallback = %q, want previous config kept", cfg.Fallback)
	}
}
