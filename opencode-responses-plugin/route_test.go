package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

func routeRequest(model, format string, stream bool) pluginapi.ModelRouteRequest {
	return pluginapi.ModelRouteRequest{
		SourceFormat:   format,
		RequestedModel: model,
		Stream:         stream,
		Body:           []byte(`{"model":"` + model + `","input":"hi"}`),
	}
}

func testConfig() pluginConfig {
	return pluginConfig{
		Enabled: true,
		BaseURL: defaultBaseURL,
		APIKey:  "sk-test",
		Models:  []string{"muse-spark-1.2-contributor"},
	}
}

func TestDecide_RouteMatrix(t *testing.T) {
	cfg := testConfig()
	cases := []struct {
		name string
		req  pluginapi.ModelRouteRequest
		cfg  *pluginConfig
		want pluginapi.ModelRouteResponse
	}{
		{
			name: "responses request for listed model routes to executor",
			req:  routeRequest("muse-spark-1.2-contributor", "openai-response", false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "muse-spark-1.2-contributor"},
		},
		{
			name: "thinking suffix model hits",
			req:  routeRequest("muse-spark-1.2-contributor(high)", "openai-response", false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "muse-spark-1.2-contributor(high)"},
		},
		{
			name: "case-insensitive model match",
			req:  routeRequest("Muse-Spark-1.2-Contributor", "openai-response", false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "Muse-Spark-1.2-Contributor"},
		},
		{
			name: "configured entry with suffix still matches bare model",
			req:  routeRequest("muse-spark-1.2-contributor", "openai-response", false),
			cfg:  &pluginConfig{Enabled: true, BaseURL: defaultBaseURL, APIKey: "sk-test", Models: []string{"Muse-Spark-1.2-Contributor(low)"}},
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "muse-spark-1.2-contributor"},
		},
		{
			name: "streaming request hits identically",
			req:  routeRequest("muse-spark-1.2-contributor", "openai-response", true),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "muse-spark-1.2-contributor"},
		},
		{
			name: "chat-completions source format handled",
			req:  routeRequest("muse-spark-1.2-contributor", "openai", false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "muse-spark-1.2-contributor"},
		},
		{
			name: "chat format case-insensitive",
			req:  routeRequest("muse-spark-1.2-contributor", "OpenAI", false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "muse-spark-1.2-contributor"},
		},
		{
			name: "chat format with thinking suffix",
			req:  routeRequest("muse-spark-1.2-contributor(high)", "openai", false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetExecutor, Target: pluginID, TargetModel: "muse-spark-1.2-contributor(high)"},
		},
		{
			name: "chat-only model not handled",
			req:  routeRequest("mimo-v2.5", "openai-response", false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "empty model not handled",
			req:  routeRequest("", "openai-response", false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "disabled config not handled",
			req:  routeRequest("muse-spark-1.2-contributor", "openai-response", false),
			cfg:  &pluginConfig{Enabled: false, BaseURL: defaultBaseURL, APIKey: "sk-test", Models: []string{"muse-spark-1.2-contributor"}},
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "empty models list not handled",
			req:  routeRequest("muse-spark-1.2-contributor", "openai-response", false),
			cfg:  &pluginConfig{Enabled: true, BaseURL: defaultBaseURL, APIKey: "sk-test"},
			want: pluginapi.ModelRouteResponse{Handled: false},
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

func TestUpstreamURL(t *testing.T) {
	cfg := testConfig()
	if got := upstreamURL(cfg, ""); got != "https://opencode.ai/zen/go/v1/responses" {
		t.Fatalf("upstreamURL() = %q", got)
	}
	cfg.BaseURL = "https://example.com/v1/"
	if got := upstreamURL(cfg, ""); got != "https://example.com/v1/responses" {
		t.Fatalf("upstreamURL() trailing slash = %q", got)
	}
	if got := upstreamURL(cfg, "responses/compact"); got != "https://example.com/v1/responses/compact" {
		t.Fatalf("upstreamURL() compact = %q", got)
	}
}

func TestEnsureStreamFlag(t *testing.T) {
	payload := []byte(`{"model":"m","input":"hi"}`)
	if got := string(ensureStreamFlag(payload, true)); got != `{"model":"m","input":"hi","stream":true}` {
		t.Fatalf("ensureStreamFlag(true) = %s", got)
	}
	withStream := []byte(`{"model":"m","stream":true}`)
	if got := string(ensureStreamFlag(withStream, false)); got != `{"model":"m","stream":false}` {
		t.Fatalf("ensureStreamFlag(false) = %s", got)
	}
	if got := string(ensureStreamFlag(withStream, true)); got != `{"model":"m","stream":true}` {
		t.Fatalf("ensureStreamFlag(no-op) = %s", got)
	}
	if got := string(ensureStreamFlag([]byte("not json"), true)); got != "not json" {
		t.Fatalf("ensureStreamFlag(invalid) = %s", got)
	}
}

func TestChatToResponses(t *testing.T) {
	payload := []byte(`{"model":"muse-spark-1.2-contributor","messages":[{"role":"user","content":"hello"},{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"https://example.com/img.png"}}]}],"stream":false,"temperature":0.7,"max_tokens":100}`)
	out := chatToResponses(payload, "muse-spark-1.2-contributor", false)
	if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.2-contributor" {
		t.Fatalf("model = %q", got)
	}
	if got := gjson.GetBytes(out, "stream").Bool(); got != false {
		t.Fatalf("stream = %v", got)
	}
	if got := gjson.GetBytes(out, "temperature").Float(); got != 0.7 {
		t.Fatalf("temperature = %v", got)
	}
	if got := gjson.GetBytes(out, "max_output_tokens").Int(); got != 100 {
		t.Fatalf("max_output_tokens = %d", got)
	}
	input := gjson.GetBytes(out, "input")
	if !input.IsArray() || len(input.Array()) != 2 {
		t.Fatalf("input = %s", input.Raw)
	}
	if got := input.Get("0.role").String(); got != "user" {
		t.Fatalf("input.0.role = %q", got)
	}
	if got := input.Get("0.content.0.type").String(); got != "input_text" {
		t.Fatalf("input.0.content.0.type = %q", got)
	}
	if got := input.Get("0.content.0.text").String(); got != "hello" {
		t.Fatalf("input.0.content.0.text = %q", got)
	}
	if got := input.Get("1.content.0.type").String(); got != "input_text" {
		t.Fatalf("input.1.content.0.type = %q", got)
	}
	if got := input.Get("1.content.1.type").String(); got != "input_image" {
		t.Fatalf("input.1.content.1.type = %q", got)
	}
	if got := input.Get("1.content.1.image_url").String(); got != "https://example.com/img.png" {
		t.Fatalf("input.1.content.1.image_url = %q", got)
	}
}

func TestResponsesToChatNonStream(t *testing.T) {
	resp := []byte(`{"id":"resp_123","object":"response","created_at":1234567890,"model":"muse-spark-1.2-contributor","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}],"usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12}}`)
	out := responsesToChatNonStream(resp, "muse-spark-1.2-contributor")
	if got := gjson.GetBytes(out, "object").String(); got != "chat.completion" {
		t.Fatalf("object = %q", got)
	}
	if got := gjson.GetBytes(out, "choices.0.message.content").String(); got != "hello world" {
		t.Fatalf("content = %q", got)
	}
	if got := gjson.GetBytes(out, "choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("finish_reason = %q", got)
	}
	if got := gjson.GetBytes(out, "usage.prompt_tokens").Int(); got != 5 {
		t.Fatalf("prompt_tokens = %d", got)
	}
	if got := gjson.GetBytes(out, "usage.completion_tokens").Int(); got != 7 {
		t.Fatalf("completion_tokens = %d", got)
	}
}

func TestTranslateResponsesStreamToChat(t *testing.T) {
	state := &chatStreamState{model: "muse-spark-1.2-contributor"}
	payload := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
	out := translateResponsesStreamToChat(payload, state, "muse-spark-1.2-contributor")
	if len(out) == 0 {
		t.Fatalf("expected translated chunks")
	}
	found := false
	for _, chunk := range out {
		if gjson.GetBytes(chunk, "choices.0.delta.content").String() == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("delta not translated, out=%s", string(bytesJoin(out)))
	}
}

func bytesJoin(chunks [][]byte) []byte {
	var b []byte
	for _, c := range chunks {
		b = append(b, c...)
	}
	return b
}
