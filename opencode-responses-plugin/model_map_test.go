package main

import (
	"testing"

	"github.com/tidwall/gjson"
)

// TestModelMapRewrite verifies transparent model alias rewriting via model-map.
func TestModelMapRewrite(t *testing.T) {
	// Save and restore global config to avoid polluting other tests.
	orig := currentConfig()
	defer configStore.Store(orig)

	// Setup a map with alias -> upstream.
	mapped := map[string]string{
		"muse-spark-1.2-contributor": "muse-spark-1.3-contributor",
	}
	// Helper to set config store for each subtest.
	setMap := func(m map[string]string) {
		cfg := currentConfig()
		cfg.Enabled = true
		cfg.ModelMap = m
		// normalize for storage (simulate parse path)
		cfg.ModelMap = normalizeModelMap(cfg.ModelMap)
		configStore.Store(cfg)
	}

	t.Run("alias in map rewrites upstream model via chatToResponses", func(t *testing.T) {
		setMap(mapped)
		payload := []byte(`{"model":"muse-spark-1.2-contributor","messages":[{"role":"user","content":"hi"}]}`)
		out := chatToResponses(payload, "", false)
		if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.3-contributor" {
			t.Fatalf("model = %q, want muse-spark-1.3-contributor; body=%s", got, string(out))
		}
	})

	t.Run("alias with thinking suffix rewrites and preserves effort", func(t *testing.T) {
		setMap(mapped)
		payload := []byte(`{"model":"muse-spark-1.2-contributor(high)","messages":[{"role":"user","content":"hi"}]}`)
		out := chatToResponses(payload, "", false)
		if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.3-contributor" {
			t.Fatalf("model = %q, want mapped upstream; body=%s", got, string(out))
		}
		if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "high" {
			t.Fatalf("reasoning.effort = %q, want high; body=%s", got, string(out))
		}
	})

	t.Run("normalizeUpstreamModel rewrites alias", func(t *testing.T) {
		setMap(mapped)
		payload := []byte(`{"model":"muse-spark-1.2-contributor","input":"hi"}`)
		out := normalizeUpstreamModel(payload)
		if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.3-contributor" {
			t.Fatalf("model = %q, want mapped; body=%s", got, string(out))
		}
	})

	t.Run("normalizeUpstreamModel rewrites alias with suffix", func(t *testing.T) {
		setMap(mapped)
		payload := []byte(`{"model":"muse-spark-1.2-contributor(max)","input":"hi"}`)
		out := normalizeUpstreamModel(payload)
		if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.3-contributor" {
			t.Fatalf("model = %q, want mapped; body=%s", got, string(out))
		}
		if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "max" {
			t.Fatalf("reasoning.effort = %q, want max; body=%s", got, string(out))
		}
	})

	t.Run("rewritePayloadModel rewrites alias", func(t *testing.T) {
		setMap(mapped)
		payload := []byte(`{"model":"muse-spark-1.2-contributor","input":"hi","stream":false}`)
		out := rewritePayloadModel(payload)
		if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.3-contributor" {
			t.Fatalf("model = %q, want mapped; body=%s", got, string(out))
		}
	})

	t.Run("unmapped model passes through", func(t *testing.T) {
		setMap(mapped)
		payload := []byte(`{"model":"other-model","messages":[{"role":"user","content":"hi"}]}`)
		out := chatToResponses(payload, "", false)
		if got := gjson.GetBytes(out, "model").String(); got != "other-model" {
			t.Fatalf("model = %q, want other-model unchanged; body=%s", got, string(out))
		}
		payload2 := []byte(`{"model":"other-model","input":"hi"}`)
		out2 := normalizeUpstreamModel(payload2)
		if got := gjson.GetBytes(out2, "model").String(); got != "other-model" {
			t.Fatalf("normalize unmapped = %q, want other-model; body=%s", got, string(out2))
		}
		payload3 := []byte(`{"model":"other-model","input":"hi"}`)
		out3 := rewritePayloadModel(payload3)
		if got := gjson.GetBytes(out3, "model").String(); got != "other-model" {
			t.Fatalf("rewritePayload unmapped = %q, want other-model; body=%s", got, string(out3))
		}
	})

	t.Run("empty map passes through", func(t *testing.T) {
		setMap(nil)
		payload := []byte(`{"model":"muse-spark-1.2-contributor","messages":[{"role":"user","content":"hi"}]}`)
		out := chatToResponses(payload, "", false)
		if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.2-contributor" {
			t.Fatalf("model = %q, want original with empty map; body=%s", got, string(out))
		}
		payload2 := []byte(`{"model":"muse-spark-1.2-contributor","input":"hi"}`)
		out2 := normalizeUpstreamModel(payload2)
		// Without map, normalizeUpstreamModel should not rewrite plain alias (no suffix)
		// So payload should be unchanged (returned as-is)
		if string(out2) != string(payload2) {
			// If suffix case, it would strip, but plain should be unchanged
			if gjson.GetBytes(out2, "model").String() != "muse-spark-1.2-contributor" {
				t.Fatalf("empty map should not rewrite: %s", string(out2))
			}
		}
		payload3 := []byte(`{"model":"muse-spark-1.2-contributor","input":"hi"}`)
		out3 := rewritePayloadModel(payload3)
		if got := gjson.GetBytes(out3, "model").String(); got != "muse-spark-1.2-contributor" {
			t.Fatalf("rewrite empty map = %q, want original; body=%s", got, string(out3))
		}
	})

	t.Run("case-insensitive lookup", func(t *testing.T) {
		setMap(map[string]string{"Muse-Spark-1.2-Contributor": "muse-spark-1.3-contributor"})
		payload := []byte(`{"model":"muse-spark-1.2-contributor","messages":[{"role":"user","content":"hi"}]}`)
		out := chatToResponses(payload, "", false)
		if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.3-contributor" {
			t.Fatalf("case-insensitive model = %q, want mapped; body=%s", got, string(out))
		}
	})
}

func TestNormalizeModelMap(t *testing.T) {
	t.Run("trim and skip empty", func(t *testing.T) {
		m := map[string]string{
			" a ": " b ",
			"":    "x",
			"y":   "",
			"  ":  "  ",
		}
		out := normalizeModelMap(m)
		if len(out) != 1 {
			t.Fatalf("out = %v, want 1 entry", out)
		}
		if out["a"] != "b" {
			t.Fatalf("out = %v, want a->b", out)
		}
	})

	t.Run("skip equal case-insensitively", func(t *testing.T) {
		m := map[string]string{
			"Same": "same",
			"foo":  "Foo",
			"bar":  "baz",
		}
		out := normalizeModelMap(m)
		if len(out) != 1 || out["bar"] != "baz" {
			t.Fatalf("out = %v, want only bar->baz", out)
		}
	})

	t.Run("deduplicate case-insensitively", func(t *testing.T) {
		m := map[string]string{
			"Model-A": "upstream-1",
			"model-a": "upstream-2",
		}
		out := normalizeModelMap(m)
		if len(out) != 1 {
			t.Fatalf("out = %v, want 1 deduplicated", out)
		}
		// Should keep first occurrence (unordered map, but dedup should ensure only one)
	})

	t.Run("valid map passes", func(t *testing.T) {
		m := map[string]string{
			"muse-spark-1.2-contributor": "muse-spark-1.3-contributor",
		}
		out := normalizeModelMap(m)
		if out["muse-spark-1.2-contributor"] != "muse-spark-1.3-contributor" {
			t.Fatalf("out = %v, want mapping", out)
		}
	})
}

func TestParsePluginConfig_ModelMap(t *testing.T) {
	raw := []byte("enabled: true\nmodel-map:\n  \" muse-spark-1.2-contributor \": \" muse-spark-1.3-contributor \"\n  \"same\": \"same\"\n  \"\": \"x\"\n")
	cfg := parsePluginConfig(raw)
	if len(cfg.ModelMap) != 1 {
		t.Fatalf("ModelMap = %v, want 1 valid entry", cfg.ModelMap)
	}
	if cfg.ModelMap["muse-spark-1.2-contributor"] != "muse-spark-1.3-contributor" {
		t.Fatalf("ModelMap = %v, want trimmed mapping", cfg.ModelMap)
	}
}
