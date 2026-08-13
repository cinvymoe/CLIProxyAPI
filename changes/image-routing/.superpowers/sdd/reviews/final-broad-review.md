# Final Broad Review - image-routing whole change

**Reviewer**: Oracle (read-only)
**Base**: ec1f6878
**Head**: c4e8a1ab
**Branch**: dev
**Waves in scope**: w1-skeleton-detect, w2-decision, w3-build-e2e (incl. repair commits)
**Prior wave reviews**: w1-skeleton-detect.md, w2-decision.md, w1-repair-re-review.md, w2-repair-re-review.md, w3-build-e2e.md (all PASS - not re-adjudicated; this review verifies the whole satisfies spec/contract).
**Date**: 2026-08-05

## Verdict: PASS

One-line summary: All 8 spec requirements are implemented and verifiable in the plugin; zero mainline changes confirmed; scope fence respected; 31 tests pass with gofmt/vet clean; e2e evidence (hit-response routed to opencode-go, text-response miss path) is internally consistent with the contract acceptance criteria; README and design decisions match the implementation.

## Per-check results

### 1. Spec coverage (8 requirements) - PASS

| # | Requirement | Location | Verifiable |
|---|---|---|---|
| R1 | Config parsing (enabled default false; missing=zero; invalid YAML keeps previous; first-load=default) | config.go:24-26 (defaultConfig false), :58-67 (parseRoutingConfig returns currentConfig on YAML error -> defaultConfig on first load) | Tests: FullConfig, MissingFields, EnabledMissingDefaultsFalse, InvalidYAMLKeepsPrevious ✓ |
| R2 | chat-completions detection (SourceFormat="openai"; messages[].content[] type=="image_url"; tool msgs; string content=no match) | detect.go:15-16 (case "openai"), :30-39 (detectImageChatCompletions) | 4 subtests incl. tool-result image_url and plain-string-content ✓ |
| R3 | responses detection (SourceFormat="openai-response"; input[]/content[] type=="input_image") | detect.go:17-18, :43-54 | 2 subtests ✓ |
| R4 | Claude detection (type=="image") | detect.go:19-20, :58-66 | 2 subtests ✓ |
| R5 | Gemini detection (inlineData/fileData) | detect.go:21-22, :70-78 | 3 subtests (inlineData, fileData, text-only) ✓ |
| R6 | Decision (enabled+non-empty fields; suffix-stripped case-insensitive model in list; detectImage; fallback-provider available via prefix-tolerant match -> Handled=true with actual available key as Target, TargetModel=fallback; else Handled=false) | route.go:18-44 (decide), :49-58 (matchAvailableProvider: exact OR openai-compatible- prefix, case-insensitive, trimmed) | 9 matrix subtests + 3 empty-config subtests ✓ |
| R7 | Stream neutrality (Stream does not influence) | route.go never references req.Stream | "streaming request hits identically" subtest asserts identical hit result ✓ |
| R8 | Registration + envelope (model_router capability; model.route handler; register/reconfigure same structure; unknown method error envelope; no crash) | main.go:115-138 (handleMethod: register+reconfigure share branch returning registration envelope; model.route -> handleModelRoute; default -> errorEnvelope) | TestHandleMethod_RegisterDeclaresModelRouterCapability, _UnknownMethodReturnsErrorEnvelope, _ModelRouteReturnsValidEnvelope ✓ |

Host contract verification (independent of diffs):
- SourceFormat strings "openai"/"openai-response": matches `internal/constant/constant.go:20,23` and HandlerType() returns (verified in w1-repair-re-review).
- openai-compatible- prefix: matches `internal/util/provider.go:15` `openAICompatibleProviderPrefix = "openai-compatible-"` (verified in w2-repair-re-review). Plugin's local const (route.go:12) is structurally necessary (host const unexported) and documented.
- config_yaml wire shape: plugin's `lifecycleRequest{ConfigYAML []byte json:"config_yaml"}` mirrors host's `rpcLifecycleRequest` in `internal/pluginhost/rpc_schema.go:10-13` exactly.
- pluginapi types: ModelRouteRequest (types.go:525-548) and ModelRouteResponse (types.go:566-578) confirmed - Response has NO json tags, so wire format uses Go field names verbatim; host decodes the same tagless struct, round-trips correctly.
- thinking.ParseSuffix: exists at `internal/thinking/suffix.go:23`, returns SuffixResult.ModelName (types.go:84-87). Module path shares prefix, internal import legal.

### 2. Contract compliance - PASS

| Contract clause | Verification |
|---|---|
| Zero mainline changes (diff only touches image-routing-plugin/) | `git diff --name-only ec1f6878 c4e8a1ab` -> 11 files, ALL under image-routing-plugin/ (go.mod, go.sum, config.go, config_test.go, detect.go, detect_test.go, main.go, main_test.go, route.go, route_test.go, README.md). No internal/, sdk/, cmd/ paths. ✓ |
| Scope fence: no auto capability detection | Plugin uses explicit `models` list from config; no SupportedInputModalities lookup. ✓ |
| Scope fence: no /v1/images handling | No image-generation endpoint logic; detectImage only inspects chat/responses/claude/gemini request bodies. ✓ |
| Scope fence: no response force-mapping | Plugin returns ModelRouteResponse only; no response rewriting. README line 51 documents "改路后,响应与用量统计中的模型名为 fallback 模型" (no force-mapping). ✓ |
| Scope fence: no fallback chains | Single fallback-provider; no second-level fallback. Route.go returns Handled=false (not a different provider) when fallback-provider unavailable. ✓ |
| Test obligations (boundary cases) | All 11 required boundary cases covered: string content (non-array) ✓; tool message image_url ✓; input_image in message content ✓; Gemini fileData ✓; unknown SourceFormat ✓; invalid YAML keeps previous ✓ (first-load default by code inspection); enabled missing ✓; fallback-provider not in AvailableProviders ✓; thinking suffix ✓; case-insensitive ✓; streaming request ✓. |
| e2e evidence consistent with acceptance criteria | hit-response.json: "Error from provider (Console Go): Upstream request failed: [400] Multimodal data is corrupted" - proves routing fired to opencode-go (configured fallback-provider). text-response.json: 200 OK with model "deepseek-v4-flash" - proves miss path (text-only not routed). T4 report claims out-of-list (glm-5.1) not routed and disabled regression (no plugin lines) - consistent with host.go ApplyConfig skipping disabled plugins. ✓ |

### 3. Code quality whole-module - PASS

| Dimension | Result |
|---|---|
| TDD-visible test coverage | 31 test cases across 4 test files: config_test.go (4), detect_test.go (12 subtests), main_test.go (3), route_test.go (9 matrix + 3 empty-config). All PASS (verified: `go test -v ./...` -> ok). |
| gofmt clean | `gofmt -l .` -> empty (no files listed). ✓ |
| go vet clean | `go vet ./...` -> empty. ✓ |
| Dead code | None. All helpers used: containsFold (route.go), matchAvailableProvider (route.go), defaultConfig/currentConfig/parseRoutingConfig/applyConfig (config.go), okEnvelope/errorEnvelope/writeResponse (main.go). No unreachable code. |
| Imports | All imports used across main.go (encoding/json, unsafe, pluginabi, pluginapi), config.go (encoding/json, sync/atomic, yaml.v3, logrus), detect.go (strings, gjson), route.go (strings, thinking, pluginapi). |
| English comments | All source comments in English. README in Chinese (appropriate for user-facing deployer doc in this repo area; matches proposal language). ✓ |
| No secrets | No tokens/keys/credentials in code, tests, go.sum, or README. Config example uses channel names only. ✓ |
| No log.Fatal / no panics in handlers | Plugin uses logrus Infof/Warnf; handleMethod returns (errorEnvelope, nil) for unknown methods - no panic. ✓ |

### 4. Consistency (README vs implementation vs design) - PASS

| Design decision | Implementation | README |
|---|---|---|
| Decision 3 (fallback-provider required) | route.go:20 short-circuits on `cfg.FallbackProvider == ""` | README line 46: "fallback-provider:改路目标通道(必填)" ✓ |
| Decision 5 (response model = fallback model, no force-mapping) | Plugin returns routing decision only; no response rewriting | README line 51: "改路后,响应与用量统计中的模型名为 fallback 模型" ✓ |
| Decision 6 (provider-unavailable -> Handled=false) | route.go:33-36 returns notHandled when matchAvailableProvider returns "" | README line 46: "若该通道不在当前可用通道中,改路静默跳过(请求按未命中处理)" ✓ |
| Decision 4 (4-protocol detection) | detect.go switch covers openai/openai-response/claude/gemini | README lines 27-34 table lists all four ✓ |
| openai-compatible- prefix matching (repair) | route.go:49-58 matchAvailableProvider | README line 46: "插件自动兼容 openai-compat 通道的 `openai-compatible-` 前缀匹配" ✓ |
| enabled default false | config.go:25 defaultConfig() | README line 44: "插件默认禁用" ✓ |

## Findings

### Critical
None.

### Important
None.

### Minor

**M1. Wire-format comment terminology imprecise (main.go:141-145, main_test.go:65-67)**
Comments describe the ModelRouteResponse wire format as "camelCase". The actual wire format is PascalCase (Go field names verbatim: "Handled", "TargetKind", "Target", "TargetModel", "Reason") because `pluginapi.ModelRouteResponse` has no JSON tags and Go's encoding/json uses field names as-is. Behavior is correct: the host decodes the same tagless struct, so the wire round-trips regardless of what we call the casing. The test asserts via explicit `json:"Handled"` tags which match the PascalCase keys. Terminology-only imprecision; no correctness impact. Not blocking.

**M2. Stale SourceFormat in TestDecide_EmptyConfigFieldsNotHandled (route_test.go:104)**
Uses `SourceFormat: "chat-completions"` (pre-repair value) instead of "openai". Test passes only because config-field validation (`cfg.Fallback == ""`, etc.) short-circuits at route.go:20 before detectImage is called, so the stale format is never evaluated. Behavior under test is correct; the stale string is inconsistent with the rest of the suite (which was updated to "openai"). Already noted in w2-repair-re-review. Not blocking.

**M3. applyConfig resets to defaults on malformed JSON envelope (config.go:44-56)**
On `reconfigure` with a malformed outer JSON envelope (not invalid YAML inside), `applyConfig` falls through to `configStore.Store(defaultConfig())`, discarding the previous config. The spec only mandates "invalid YAML keeps previous config"; the JSON-envelope case is unspecified. The host is trusted to produce valid JSON envelopes (`internal/pluginhost/rpc_client.go` always marshals via encoding/json), so this is unlikely in practice. A more conservative `return` (keep previous) on JSON unmarshal failure would be safer. Already noted in w1-skeleton-detect review. Not blocking.

## Whole-change verification summary

- **Build/test**: `cd image-routing-plugin && gofmt -l . && go vet ./... && go test ./...` -> all clean, 31 tests PASS (verified independently in this review).
- **Zero mainline**: `git diff --name-only ec1f6878 c4e8a1ab` -> only image-routing-plugin/ files.
- **e2e artifacts**: /tmp/ir-e2e/ contains built .so (7.6 MB), built server binary, hit-response.json (routing fired to opencode-go), text-response.json (miss path 200 OK), server logs showing "pluginhost: plugin loaded" + "image-routing: config applied (enabled=true fallback=mimo-v2.5 fallback-provider=opencode-go models=[deepseek-v4-flash])".
- **Prior wave reviews**: all 5 PASS (w1-skeleton-detect, w2-decision, w1-repair-re-review, w2-repair-re-review, w3-build-e2e); findings were Minor/non-blocking and not re-adjudicated here.

## Readiness to close

**READY TO CLOSE.** The whole change satisfies all 8 spec requirements, respects the execution-contract Intent Lock and scope fence (zero mainline changes; no auto-detection; no /v1/images; no force-mapping; no fallback chains), meets all test obligations, passes gofmt/vet/test, and the e2e evidence is internally consistent with the contract acceptance criteria. The three Minor findings are documentation/terminology imprecisions and pre-existing notes from wave reviews - none affect correctness or block closure.

## Per-check summary

| Check | Result |
|---|---|
| 1. Spec coverage (8 requirements) | PASS |
| 2. Contract compliance (zero mainline, scope fence, test obligations, e2e) | PASS |
| 3. Code quality whole-module (coverage, dead code, comments, secrets, gofmt/vet) | PASS |
| 4. Consistency (README vs design vs implementation) | PASS |
