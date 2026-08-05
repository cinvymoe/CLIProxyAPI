package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestHandleMethod_RegisterDeclaresModelRouterCapability(t *testing.T) {
	raw, err := handleMethod(pluginabi.MethodPluginRegister, []byte(`{}`))
	if err != nil {
		t.Fatalf("handleMethod register: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		t.Fatalf("envelope = %+v, err %v; want ok", env, err)
	}
	var reg struct {
		Capabilities registrationCapability `json:"capabilities"`
	}
	if err := json.Unmarshal(env.Result, &reg); err != nil {
		t.Fatalf("unmarshal registration: %v", err)
	}
	if !reg.Capabilities.ModelRouter {
		t.Fatal("capabilities.model_router = false, want true")
	}
}

func TestHandleMethod_UnknownMethodReturnsErrorEnvelope(t *testing.T) {
	raw, err := handleMethod("no.such.method", nil)
	if err != nil {
		t.Fatalf("handleMethod: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code == "" {
		t.Fatalf("envelope = %+v, want error envelope with code", env)
	}
}

func TestHandleMethod_ModelRouteReturnsValidEnvelope(t *testing.T) {
	configStore.Store(routingConfig{Enabled: true, Fallback: "mimo-v2.5", FallbackProvider: "opencode-go", Models: []string{"deepseek-v4-flash"}})
	req := pluginapi.ModelRouteRequest{
		SourceFormat:       "chat-completions",
		RequestedModel:     "deepseek-v4-flash",
		Body:               []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`),
		AvailableProviders: []string{"opencode-go"},
	}
	rawRequest, _ := json.Marshal(req)
	raw, err := handleMethod(pluginabi.MethodModelRoute, rawRequest)
	if err != nil {
		t.Fatalf("handleMethod model.route: %v", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope = %+v, want ok", env)
	}
	var resp struct {
		Handled     bool   `json:"handled"`
		TargetKind  string `json:"target_kind"`
		Target      string `json:"target"`
		TargetModel string `json:"target_model"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Handled || resp.TargetKind != "provider" || resp.Target != "opencode-go" || resp.TargetModel != "mimo-v2.5" {
		t.Fatalf("resp = %+v, want handled provider opencode-go mimo-v2.5", resp)
	}
}
