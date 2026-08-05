package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func imageBody() []byte {
	return []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`)
}

func textBody() []byte {
	return []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
}

func routeRequest(model string, body []byte, format string, providers []string, stream bool) pluginapi.ModelRouteRequest {
	return pluginapi.ModelRouteRequest{
		SourceFormat:       format,
		RequestedModel:     model,
		Stream:             stream,
		Body:               body,
		AvailableProviders: providers,
	}
}

func TestDecide_RouteMatrix(t *testing.T) {
	cfg := routingConfig{Enabled: true, Fallback: "mimo-v2.5", FallbackProvider: "opencode-go", Models: []string{"deepseek-v4-flash"}}
	cases := []struct {
		name string
		req  pluginapi.ModelRouteRequest
		want pluginapi.ModelRouteResponse
		cfg  *routingConfig
	}{
		{
			name: "hit routes to fallback provider and model",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", []string{"openai-compatible-opencode-go"}, false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetProvider, Target: "openai-compatible-opencode-go", TargetModel: "mimo-v2.5"},
		},
		{
			name: "thinking suffix model hits",
			req:  routeRequest("deepseek-v4-flash(high)", imageBody(), "openai", []string{"openai-compatible-opencode-go"}, false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetProvider, Target: "openai-compatible-opencode-go", TargetModel: "mimo-v2.5"},
		},
		{
			name: "case-insensitive model match",
			req:  routeRequest("DeepSeek-V4-Flash", imageBody(), "openai", []string{"openai-compatible-opencode-go"}, false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetProvider, Target: "openai-compatible-opencode-go", TargetModel: "mimo-v2.5"},
		},
		{
			name: "model outside list not handled",
			req:  routeRequest("glm-5.1", imageBody(), "openai", []string{"openai-compatible-opencode-go"}, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "text-only body not handled",
			req:  routeRequest("deepseek-v4-flash", textBody(), "openai", []string{"openai-compatible-opencode-go"}, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "fallback provider unavailable not handled",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", []string{"openai-compatible-xfyun-coding"}, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "non-prefixed provider exact match",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", []string{"gemini"}, false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetProvider, Target: "gemini", TargetModel: "mimo-v2.5"},
			cfg:  &routingConfig{Enabled: true, Fallback: "mimo-v2.5", FallbackProvider: "gemini", Models: []string{"deepseek-v4-flash"}},
		},
		{
			name: "streaming request hits identically",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", []string{"openai-compatible-opencode-go"}, true),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetProvider, Target: "openai-compatible-opencode-go", TargetModel: "mimo-v2.5"},
		},
		{
			name: "disabled config not handled",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", []string{"openai-compatible-opencode-go"}, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
			cfg:  &routingConfig{Enabled: false, Fallback: "mimo-v2.5", FallbackProvider: "opencode-go", Models: []string{"deepseek-v4-flash"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testCfg := cfg
			if tc.cfg != nil {
				testCfg = *tc.cfg
			}
			got := decide(tc.req, testCfg)
			if got.Handled != tc.want.Handled || got.TargetKind != tc.want.TargetKind || got.Target != tc.want.Target || got.TargetModel != tc.want.TargetModel {
				t.Fatalf("decide() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecide_EmptyConfigFieldsNotHandled(t *testing.T) {
	for name, cfg := range map[string]routingConfig{
		"empty fallback": {Enabled: true, Fallback: "", FallbackProvider: "opencode-go", Models: []string{"m"}},
		"empty provider": {Enabled: true, Fallback: "f", FallbackProvider: "", Models: []string{"m"}},
		"empty models":   {Enabled: true, Fallback: "f", FallbackProvider: "p", Models: nil},
	} {
		t.Run(name, func(t *testing.T) {
			req := routeRequest("m", imageBody(), "chat-completions", []string{"p"}, false)
			if got := decide(req, cfg); got.Handled {
				t.Fatalf("decide() = %+v, want not handled", got)
			}
		})
	}
}
