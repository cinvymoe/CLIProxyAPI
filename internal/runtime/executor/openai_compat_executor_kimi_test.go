package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestNormalizeCompatTemperatureForUpstream_ForcesOneForKimiK3(t *testing.T) {
	body := []byte(`{"model":"k3-256k","temperature":0.7,"messages":[{"role":"user","content":"hello"}]}`)

	out, err := normalizeCompatTemperatureForUpstream(body, "k3-256k", "https://api.kimi.com/coding/v1")
	if err != nil {
		t.Fatalf("normalizeCompatTemperatureForUpstream() error = %v", err)
	}
	if got := gjson.GetBytes(out, "temperature").Float(); got != 1 {
		t.Fatalf("temperature = %v, want 1", got)
	}
}

func TestNormalizeCompatTemperatureForUpstream_ForcesOneForClientAlias(t *testing.T) {
	// The client-facing alias "k3" arrives in the request body while the
	// resolved model may be the upstream name (e.g. "k3-256k").
	body := []byte(`{"model":"k3","temperature":0.2,"messages":[{"role":"user","content":"hello"}]}`)

	out, err := normalizeCompatTemperatureForUpstream(body, "k3-256k", "https://api.kimi.com/coding/v1")
	if err != nil {
		t.Fatalf("normalizeCompatTemperatureForUpstream() error = %v", err)
	}
	if got := gjson.GetBytes(out, "temperature").Float(); got != 1 {
		t.Fatalf("temperature = %v, want 1", got)
	}
}

func TestNormalizeCompatTemperatureForUpstream_SkipsNonKimiBaseURL(t *testing.T) {
	body := []byte(`{"model":"k3-256k","temperature":0.7,"messages":[{"role":"user","content":"hello"}]}`)

	out, err := normalizeCompatTemperatureForUpstream(body, "k3-256k", "https://api.other.example.com/v1")
	if err != nil {
		t.Fatalf("normalizeCompatTemperatureForUpstream() error = %v", err)
	}
	if got := gjson.GetBytes(out, "temperature").Float(); got != 0.7 {
		t.Fatalf("temperature = %v, want 0.7", got)
	}
}

func TestNormalizeCompatTemperatureForUpstream_SkipsNonK3Model(t *testing.T) {
	body := []byte(`{"model":"kimi-for-coding","temperature":0.7,"messages":[{"role":"user","content":"hello"}]}`)

	out, err := normalizeCompatTemperatureForUpstream(body, "kimi-for-coding", "https://api.kimi.com/coding/v1")
	if err != nil {
		t.Fatalf("normalizeCompatTemperatureForUpstream() error = %v", err)
	}
	if got := gjson.GetBytes(out, "temperature").Float(); got != 0.7 {
		t.Fatalf("temperature = %v, want 0.7", got)
	}
}

func TestOpenAICompatExecutorExecute_ForcesTemperatureOneForKimiK3(t *testing.T) {
	var upstreamBody []byte
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", kimiRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		var errRead error
		upstreamBody, errRead = io.ReadAll(req.Body)
		if errRead != nil {
			return nil, errRead
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"chatcmpl_1","object":"chat.completion","created":0,"model":"k3-256k","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			)),
		}, nil
	}))

	executor := NewOpenAICompatExecutor("kimi-k2.7-code", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": "https://api.kimi.com/coding/v1",
		"api_key":  "test",
	}}
	const model = "k3-256k"
	payload := []byte(`{"model":"k3-256k","temperature":0.2,"messages":[{"role":"user","content":"hello"}]}`)
	_, err := executor.Execute(ctx, auth, cliproxyexecutor.Request{
		Model:   model,
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("openai"),
		Stream:          false,
		OriginalRequest: payload,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := gjson.GetBytes(upstreamBody, "temperature").Float(); got != 1 {
		t.Fatalf("upstream temperature = %v, want 1", got)
	}
	if got := gjson.GetBytes(upstreamBody, "model").String(); got != "k3-256k" {
		t.Fatalf("upstream model = %q, want k3-256k", got)
	}
}

func TestOpenAICompatExecutorExecute_PreservesTemperatureForNonKimiK3(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":0,"model":"k3-256k","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("kimi-k2.7-code", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test",
	}}
	const model = "k3-256k"
	payload := []byte(`{"model":"k3-256k","temperature":0.2,"messages":[{"role":"user","content":"hello"}]}`)
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   model,
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("openai"),
		Stream:          false,
		OriginalRequest: payload,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := gjson.GetBytes(upstreamBody, "temperature").Float(); got != 0.2 {
		t.Fatalf("upstream temperature = %v, want 0.2 (preserved for non-Kimi host)", got)
	}
}
