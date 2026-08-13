# 实施任务:image-routing 图片请求改路插件

## 交付/证明映射

| 需求(Spec) | 任务 |
|---|---|
| 插件配置解析 | T1 |
| 插件能力注册与协议合规 | T1 |
| chat-completions / responses / Claude / Gemini 图片检测 | T2 |
| 改路决策(含全部 Scenario) | T3 |
| 流式与非流式一致性 | T3(决策不读 Stream 字段) |
| 主机集成(零主线改动) | T1(机制)+ T4(端到端证明) |

## 任务清单

### T1:插件骨架与配置解析

**范围**:新建 `image-routing-plugin/` 独立 Go 模块:cgo ABI 框架、envelope 方法分发、`model_router` 能力注册、`routingConfig` 配置解析(线程安全存储)。

**依赖**:无。

**步骤**:

1. 创建 `image-routing-plugin/go.mod`:

```
module github.com/router-for-me/CLIProxyAPI/v7/image-routing-plugin

go 1.26.0

require (
	github.com/router-for-me/CLIProxyAPI/v7 v7.0.0
	github.com/sirupsen/logrus v1.9.3
	github.com/tidwall/gjson v1.18.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/router-for-me/CLIProxyAPI/v7 => ../..
```

(与 `examples/plugin/simple/go` 的 replace 机制一致。写完代码后运行 `go mod tidy`。)

2. 创建 `image-routing-plugin/main.go`(C ABI 框架,结构参照 `examples/plugin/simple/go/main.go`,方法分发只保留本插件需要的方法):

```go
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"encoding/json"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type registration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelRouter bool `json:"model_router"`
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		applyConfig(request) // replaces routingConfig; keeps last config on parse error
		return okEnvelope(registration{
			SchemaVersion: pluginabi.SchemaVersion,
			Metadata: pluginapi.Metadata{
				Name:             "image-routing",
				Version:          "0.1.0",
				Author:           "router-for-me",
				GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
				ConfigFields: []pluginapi.ConfigField{
					{Name: "fallback", Type: pluginapi.ConfigFieldTypeString, Description: "Model to route image requests to when the requested model is in models."},
					{Name: "fallback-provider", Type: pluginapi.ConfigFieldTypeString, Description: "Provider channel key that serves the fallback model (e.g. opencode-go)."},
					{Name: "models", Type: pluginapi.ConfigFieldTypeString, Description: "YAML list of model names treated as not supporting images (e.g. [deepseek-v4-flash])."},
				},
			},
			Capabilities: registrationCapability{ModelRouter: true},
		})
	case pluginabi.MethodModelRoute:
		return handleModelRoute(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func okEnvelope(v any) ([]byte, error) {
	raw, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
```

3. 创建 `image-routing-plugin/config.go`(配置解析,原子存储,解析失败保留旧配置):

```go
package main

import (
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
```

> 注:`enabled` 缺省按 `true` 处理(插件被配置即视为启用;主机侧 `plugins.configs.<id>.enabled: false` 会下发 `enabled: false`)。

4. 创建 `image-routing-plugin/config_test.go`:

```go
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
```

5. 创建 `image-routing-plugin/main_test.go`(方法分发与协议合规):

```go
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
		SourceFormat:       "openai",
		RequestedModel:     "deepseek-v4-flash",
		Body:               []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`),
		AvailableProviders: []string{"openai-compatible-opencode-go"},
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
```

> 注:上表 `handleMethod` 中 `MethodModelRoute` 分支引用 `handleModelRoute`(T3 实现);T1 阶段先实现一个返回 `Handled=false` 的占位版本并保证上述测试通过,T3 替换为完整决策。`pluginapi.ModelRouteTargetProvider` 的 JSON 值为 `"provider"`,与上表断言一致。

**可观测结果**:`image-routing-plugin/` 模块存在,`go test ./...` 通过(注册声明 `model_router`、未知方法 error envelope、配置解析全场景)。

**证据命令**:

```bash
cd image-routing-plugin && go mod tidy && gofmt -l . && go test ./...
```

(`gofmt -l .` 应无输出;如有输出先 `gofmt -w .` 再重跑。)

### T2:图片内容检测器

**范围**:`image-routing-plugin/detect.go` 的 `detectImage(body []byte, sourceFormat string) bool`,覆盖四种入口协议。

**依赖**:无(纯函数)。

**步骤**:

创建 `image-routing-plugin/detect.go`:

```go
package main

import (
	"strings"

	"github.com/tidwall/gjson"
)

// detectImage reports whether the client request body contains image content
// for the given source protocol format. The format strings are the host's
// HandlerType() values ("openai" for /v1/chat/completions, "openai-response"
// for /v1/responses, "claude", "gemini").
func detectImage(body []byte, sourceFormat string) bool {
	switch strings.ToLower(strings.TrimSpace(sourceFormat)) {
	case "openai":
		return detectImageChatCompletions(body)
	case "openai-response":
		return detectImageResponses(body)
	case "claude":
		return detectImageClaude(body)
	case "gemini":
		return detectImageGemini(body)
	default:
		return false
	}
}

// detectImageChatCompletions scans messages[].content[] for type=="image_url" blocks.
// String content (non-array) yields no match.
func detectImageChatCompletions(body []byte) bool {
	for _, msg := range gjson.GetBytes(body, "messages").Array() {
		for _, block := range msg.Get("content").Array() {
			if block.Get("type").String() == "image_url" {
				return true
			}
		}
	}
	return false
}

// detectImageResponses scans input[] elements for type=="input_image" items or
// content parts of type=="input_image".
func detectImageResponses(body []byte) bool {
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("type").String() == "input_image" {
			return true
		}
		for _, part := range item.Get("content").Array() {
			if part.Get("type").String() == "input_image" {
				return true
			}
		}
	}
	return false
}

// detectImageClaude scans messages[].content[] for type=="image" blocks.
func detectImageClaude(body []byte) bool {
	for _, msg := range gjson.GetBytes(body, "messages").Array() {
		for _, block := range msg.Get("content").Array() {
			if block.Get("type").String() == "image" {
				return true
			}
		}
	}
	return false
}

// detectImageGemini scans contents[].parts[] for inlineData or fileData keys.
func detectImageGemini(body []byte) bool {
	for _, content := range gjson.GetBytes(body, "contents").Array() {
		for _, part := range content.Get("parts").Array() {
			if part.Get("inlineData").Exists() || part.Get("fileData").Exists() {
				return true
			}
		}
	}
	return false
}
```

创建 `image-routing-plugin/detect_test.go`(表驱动,覆盖 specs 全部检测 Scenario):

```go
package main

import "testing"

func TestDetectImage(t *testing.T) {
	cases := []struct {
		name         string
		format       string
		body         string
		wantDetected bool
	}{
		{
			name:   "openai chat image_url",
			format: "openai",
			body:   `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:   "openai chat plain string content",
			format: "openai",
			body:   `{"messages":[{"role":"user","content":"hi"}]}`,
			wantDetected: false,
		},
		{
			name:   "openai chat text-only blocks",
			format: "openai",
			body:   `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			wantDetected: false,
		},
		{
			name:   "openai chat tool result image",
			format: "openai",
			body:   `{"messages":[{"role":"tool","tool_call_id":"t1","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:   "openai-response input_image in content",
			format: "openai-response",
			body:   `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`,
			wantDetected: true,
		},
		{
			name:   "openai-response text only",
			format: "openai-response",
			body:   `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			wantDetected: false,
		},
		{
			name:   "claude image block",
			format: "claude",
			body:   `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:   "claude text only",
			format: "claude",
			body:   `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			wantDetected: false,
		},
		{
			name:   "gemini inlineData",
			format: "gemini",
			body:   `{"contents":[{"role":"user","parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:   "gemini fileData",
			format: "gemini",
			body:   `{"contents":[{"role":"user","parts":[{"fileData":{"fileUri":"gs://bucket/a.png","mimeType":"image/png"}}]}]}`,
			wantDetected: true,
		},
		{
			name:   "gemini text only",
			format: "gemini",
			body:   `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			wantDetected: false,
		},
		{
			name:         "unknown format",
			format:       "unknown-format",
			body:         `{"messages":[{"content":[{"type":"image_url"}]}]}`,
			wantDetected: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectImage([]byte(tc.body), tc.format); got != tc.wantDetected {
				t.Fatalf("detectImage(%s) = %v, want %v", tc.format, got, tc.wantDetected)
			}
		})
	}
}
```

**可观测结果**:四协议检测正反例全部通过。

**证据命令**:

```bash
cd image-routing-plugin && go test -run TestDetectImage ./...
```

### T3:路由决策与 model.route 接线

**范围**:`image-routing-plugin/route.go` 的 `decide` 决策函数 + `handleModelRoute` 接线,替换 T1 占位实现;决策矩阵单测。

**依赖**:T1(配置)、T2(检测)。

**步骤**:

1. 创建 `image-routing-plugin/route.go`:

```go
package main

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// openAICompatProviderPrefix is how openai-compatibility channels register in
// the host's AvailableProviders (see internal/util/provider.go).
const openAICompatProviderPrefix = "openai-compatible-"

// decide implements the model.route decision for image routing.
// It returns Handled=true only when the requested model is declared as not
// supporting images, the request body contains image content, and the
// configured fallback provider is currently available.
func decide(req pluginapi.ModelRouteRequest, cfg routingConfig) pluginapi.ModelRouteResponse {
	notHandled := pluginapi.ModelRouteResponse{Handled: false}
	if !cfg.Enabled || cfg.Fallback == "" || cfg.FallbackProvider == "" || len(cfg.Models) == 0 {
		return notHandled
	}
	base := strings.TrimSpace(thinking.ParseSuffix(req.RequestedModel).ModelName)
	if base == "" {
		base = strings.TrimSpace(req.RequestedModel)
	}
	if !containsFold(cfg.Models, base) {
		return notHandled
	}
	if !detectImage(req.Body, req.SourceFormat) {
		return notHandled
	}
	target := matchAvailableProvider(req.AvailableProviders, cfg.FallbackProvider)
	if target == "" {
		return notHandled
	}
	return pluginapi.ModelRouteResponse{
		Handled:     true,
		TargetKind:  pluginapi.ModelRouteTargetProvider,
		Target:      target,
		TargetModel: cfg.Fallback,
		Reason:      "image request routed to configured fallback model",
	}
}

// matchAvailableProvider resolves the configured fallback-provider against the
// host's AvailableProviders list. openai-compat channels register with an
// "openai-compatible-" prefix, so a configured channel name matches either the
// exact key or the prefixed key. The actual available key is returned so the
// host's HasBuiltinProvider check passes.
func matchAvailableProvider(available []string, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return ""
	}
	for _, entry := range available {
		entry = strings.TrimSpace(entry)
		if strings.EqualFold(entry, configured) {
			return entry
		}
		if strings.EqualFold(entry, openAICompatProviderPrefix+configured) {
			return entry
		}
	}
	return ""
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}
```

2. 在 `main.go` 增加接线(替换 T1 占位):

```go
func handleModelRoute(request []byte) ([]byte, error) {
	var req pluginapi.ModelRouteRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	return okEnvelope(decide(req, currentConfig()))
}
```

(注意 `pluginapi` 与 `json` 在 T1 的 `main.go` 中已导入。)

3. 创建 `image-routing-plugin/route_test.go`(覆盖 specs"改路决策"全部 Scenario + 流式一致性):

```go
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
		SourceFormat:      format,
		RequestedModel:    model,
		Stream:            stream,
		Body:              body,
		AvailableProviders: providers,
	}
}

func TestDecide_RouteMatrix(t *testing.T) {
	cfg := routingConfig{Enabled: true, Fallback: "mimo-v2.5", FallbackProvider: "opencode-go", Models: []string{"deepseek-v4-flash"}}
	avail := []string{"openai-compatible-opencode-go"}
	hit := pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetProvider, Target: "openai-compatible-opencode-go", TargetModel: "mimo-v2.5"}
	cases := []struct {
		name string
		req  pluginapi.ModelRouteRequest
		want pluginapi.ModelRouteResponse
	}{
		{
			name: "hit routes to fallback provider and model",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", avail, false),
			want: hit,
		},
		{
			name: "thinking suffix model hits",
			req:  routeRequest("deepseek-v4-flash(high)", imageBody(), "openai", avail, false),
			want: hit,
		},
		{
			name: "case-insensitive model match",
			req:  routeRequest("DeepSeek-V4-Flash", imageBody(), "openai", avail, false),
			want: hit,
		},
		{
			name: "non-prefixed provider exact match",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", []string{"gemini"}, false),
			want: pluginapi.ModelRouteResponse{Handled: true, TargetKind: pluginapi.ModelRouteTargetProvider, Target: "gemini", TargetModel: "mimo-v2.5"},
		},
		{
			name: "model outside list not handled",
			req:  routeRequest("glm-5.1", imageBody(), "openai", avail, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "text-only body not handled",
			req:  routeRequest("deepseek-v4-flash", textBody(), "openai", avail, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "fallback provider unavailable not handled",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", []string{"openai-compatible-xfyun-coding"}, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
		{
			name: "streaming request hits identically",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", avail, true),
			want: hit,
		},
		{
			name: "disabled config not handled",
			req:  routeRequest("deepseek-v4-flash", imageBody(), "openai", avail, false),
			want: pluginapi.ModelRouteResponse{Handled: false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testCfg := cfg
			if tc.name == "disabled config not handled" {
				testCfg.Enabled = false
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
		"empty fallback":         {Enabled: true, Fallback: "", FallbackProvider: "opencode-go", Models: []string{"m"}},
		"empty provider":         {Enabled: true, Fallback: "f", FallbackProvider: "", Models: []string{"m"}},
		"empty models":           {Enabled: true, Fallback: "f", FallbackProvider: "p", Models: nil},
	} {
		t.Run(name, func(t *testing.T) {
			req := routeRequest("m", imageBody(), "chat-completions", []string{"p"}, false)
			if got := decide(req, cfg); got.Handled {
				t.Fatalf("decide() = %+v, want not handled", got)
			}
		})
	}
}
```

**可观测结果**:决策矩阵(含后缀、大小写、provider 不可用、禁用、流式)全部通过;`handleMethod` 的 `model.route` 返回合法 envelope。

**证据命令**:

```bash
cd image-routing-plugin && go test ./...
```

### T4:构建、文档与端到端验证

**范围**:构建脚本/命令、`image-routing-plugin/README.md`、端到端改路验证(启用与禁用两种状态)。

**依赖**:T1、T2、T3。

**步骤**:

1. 创建 `image-routing-plugin/README.md`(中文,面向部署者),内容:
   - 功能:请求含图片内容且模型在 `models` 列表时,改路到 `fallback-provider` 通道的 `fallback` 模型;未命中时行为与未安装完全一致。
   - 支持入口协议:chat-completions(`image_url`)、responses(`input_image`)、Claude(`image`)、Gemini(`inlineData`/`fileData`)。
   - 构建(需要 CGO 与 Go 1.26+):
     ```bash
     cd image-routing-plugin && go mod tidy && CGO_ENABLED=1 go build -buildmode=c-shared -o ../plugins/image-routing-v0.1.0.so .
     ```
   - 安装:将 `plugins/image-routing-v0.1.0.so` 放到服务运行目录的 `plugins/` 下(文件名即插件 ID 与版本,见主机发现规则;`plugins/` 默认被 gitignore)。
   - 配置(置于 `config.yaml`):
     ```yaml
     plugins:
       configs:
         image-routing:
           enabled: true
           fallback: mimo-v2.5
           fallback-provider: opencode-go
           models: [deepseek-v4-flash]
     ```
     字段说明:`enabled` 必填为 `true` 才启用(主机默认禁用插件;`plugins.enabled` 也需为 true);`fallback` 改路目标模型;`fallback-provider` 目标通道(必填,服务需有该通道凭证;不在可用通道时改路静默跳过);`models` 视为不支持图片的模型列表(去思考后缀、大小写不敏感)。
   - 说明:改路后响应/用量中的模型名为 fallback 模型;需要 CGO 的主程序构建(no-plugin 构建不加载插件)。

2. 本地构建验证:

```bash
cd image-routing-plugin && go mod tidy && CGO_ENABLED=1 go build -buildmode=c-shared -o /tmp/image-routing-v0.1.0.so . && ls -la /tmp/image-routing-v0.1.0.so
```

3. 端到端验证(使用本机或 Docker 部署,`rebuild.sh` 重启):
   - 将 .so 放入服务 `plugins/` 目录;`config.yaml` 增加上述 `plugins.configs.image-routing` 配置(含 `enabled: true`、`fallback: mimo-v2.5`、`fallback-provider: <实际通道>`,该通道必须已配置 mimo-v2.5 且凭证可用;本地验证可先用测试通道)。
   - 重启服务;确认日志出现插件加载与配置应用记录(`image-routing: config applied ...`)。
   - **命中场景**:构造含图请求并检查实际执行模型为 fallback 模型:
     ```bash
     curl -s http://localhost:8317/v1/chat/completions \
       -H "Content-Type: application/json" -H "Authorization: Bearer test-api-key" \
       -d '{"model":"deepseek-v4-flash","stream":false,"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}]}]}'
     ```
     断言:响应 `model` 字段为 fallback 模型(如 `mimo-v2.5`);若通道转换器会改写响应模型名,则以请求日志中实际执行模型为准(`grep` 日志中 `image-routing` 路由后执行的模型/通道)。
   - **不命中场景**:同请求去掉 `image_url` 块 → 响应 `model` 字段仍为 `deepseek-v4-flash`。
   - **列表外场景**:`"model":"glm-5.1"` 含图请求 → 模型仍为 `glm-5.1`。
   - **禁用回归**:`plugins.configs.image-routing.enabled: false` 重启 → 含图请求模型仍为 `deepseek-v4-flash`,且无改路报错(证明零主线影响)。

4. 将 e2e 实际输出(响应 model 字段、日志行)附在任务完成说明中。

5. 零主线改动验证(本变更的核心承诺):
   ```bash
   git status --porcelain -- internal/ sdk/ cmd/
   git diff --stat -- internal/ sdk/ cmd/
   ```
   断言:两个命令均无输出(主线目录零改动);`git status --porcelain` 仅应出现 `changes/image-routing/` 与 `image-routing-plugin/` 相关条目。

**可观测结果**:.so 构建成功;插件加载与配置生效;四种场景(命中/不命中/列表外/禁用)行为符合预期;主线目录无任何改动。

**证据命令**:上述 curl 命令的实际输出 + 服务日志摘录。

## 完成状态(由执行阶段标记)

- [x] T1 插件骨架与配置解析(commits be964849, 66a46799)
- [x] T2 图片内容检测器(commit a47a41d7)
- [x] T3 路由决策与 model.route 接线(commit 3de28b94)
- [x] T4 构建、文档与端到端验证(commits c85bfeb8, c4e8a1ab)
