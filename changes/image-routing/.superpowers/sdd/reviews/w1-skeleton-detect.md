# Wave w1-skeleton-detect Review Report

**Base:** ec1f6878
**Head:** a47a41d7
**Commits:** be964849 (T1 skeleton+config), 66a46799 (wire-format fix), a47a41d7 (T2 detector)
**Reviewer:** Oracle
**Date:** 2026-08-05

## Verdict: PASS

One-line summary: T1+T2 implementation is spec-compliant across all six key checks; only Minor findings remain (none blocking).

## Per-check results

### 1. Spec compliance of detectImage (four protocols) — PASS

Verified `image-routing-plugin/detect.go` against the spec scenarios:

| Protocol | Spec requirement | Implementation | Tests |
|---|---|---|---|
| chat-completions | `messages[].content[] type=="image_url"`; tool messages included; string content = no match | `detectImageChatCompletions` iterates `messages` -> `content` arrays, matches `type=="image_url"`. `gjson .Array()` on a string content yields empty slice -> no match. Tool messages iterated identically (no role filter). | 4 cases: image_url positive, plain string negative, text-only negative, tool-result image_url positive. |
| responses | `input[]/content[] type=="input_image"` | `detectImageResponses` checks top-level `input[].type=="input_image"` and `input[].content[].type=="input_image"`. | 2 cases: input_image in content positive, text-only negative. |
| claude | `messages[].content[] type=="image"` | `detectImageClaude` matches `type=="image"`. | 2 cases: image block positive, text-only negative. |
| gemini | `contents[].parts[]` `inlineData`/`fileData` keys | `detectImageGemini` checks `part.Get("inlineData").Exists() || part.Get("fileData").Exists()`. | 3 cases: inlineData positive, fileData positive, text-only negative. |
| unknown format | -> false | `default: return false` | 1 case: unknown-format negative. |

Total: 12 table-driven subtests, all matching spec scenarios. Format matching uses `strings.ToLower(strings.TrimSpace(...))` which is more lenient than strict equality (handles host-side case variation) — acceptable.

### 2. Config parsing semantics — PASS

`image-routing-plugin/config.go`:

- **enabled defaults false**: `defaultConfig()` returns `routingConfig{Enabled: false}`; `parseRoutingConfig` seeds `cfg` with `defaultConfig()` before `yaml.Unmarshal`, so an omitted `enabled` stays false. Test `TestParseRoutingConfig_EnabledMissingDefaultsFalse` verifies.
- **missing fields zero values**: `parseRoutingConfig([]byte("enabled: true"))` leaves `Fallback`/`FallbackProvider` empty and `Models` nil. Test `TestParseRoutingConfig_MissingFields` verifies.
- **invalid YAML keeps previous**: `parseRoutingConfig` returns `currentConfig()` on `yaml.Unmarshal` error. Test `TestParseRoutingConfig_InvalidYAMLKeepsPrevious` stores a sentinel config and verifies it survives an invalid-YAML parse.
- **first-load failure -> defaults**: On first load `configStore` is empty; `currentConfig()` falls through to `defaultConfig()`. `applyConfig` stores that. Correct.
- **lifecycleRequest base64 shape**: `lifecycleRequest{ConfigYAML []byte json:"config_yaml"}` mirrors host-side `rpcLifecycleRequest` in `internal/pluginhost/rpc_schema.go` (line 10-13) exactly. `encoding/json` decodes a JSON string into `[]byte` via base64, so `{"config_yaml":"<base64-yaml>"}` produces the raw YAML bytes. Verified host-side struct shape matches.

### 3. Registration envelope + unknown-method error — PASS

`image-routing-plugin/main.go`:

- **model_router capability true**: `registrationCapability{ModelRouter: true}` marshaled under `json:"capabilities.model_router"`. Test `TestHandleMethod_RegisterDeclaresModelRouterCapability` decodes the envelope and asserts `reg.Capabilities.ModelRouter == true`.
- **metadata + config fields present**: `pluginapi.Metadata{Name, Version, Author, GitHubRepository, ConfigFields}` with three ConfigField entries (`fallback`, `fallback-provider`, `models`) all typed `ConfigFieldTypeString`.
- **unknown-method error envelope**: `default: return errorEnvelope("unknown_method", "unknown method: "+method), nil`. Test `TestHandleMethod_UnknownMethodReturnsErrorEnvelope` asserts `env.OK == false` and `env.Error.Code != ""`.

### 4. Wire format (ModelRouteResponse camelCase) — PASS

Verified via focused inspection of `sdk/pluginapi/types.go` (lines 566-578): `ModelRouteResponse` struct fields (`Handled`, `TargetKind`, `Target`, `TargetModel`, `Reason`) have **no JSON tags**. Go's `encoding/json` marshals untaged fields with their Go field names (PascalCase -> JSON camelCase initial lower: `handled`, `targetKind`, `target`, `targetModel`, `reason`).

`handleModelRoute` marshals `pluginapi.ModelRouteResponse` directly via `okEnvelope(resp)` — no custom struct, no snake_case tags. The host's `decodeEnvelopeResult[pluginapi.ModelRouteResponse]` (per review brief) decodes the same tagless struct, so the wire format round-trips.

`TestHandleMethod_ModelRouteReturnsValidEnvelope` verifies twice: once with explicit `json:"Handled"`/`json:"TargetKind"`/etc. tags (mirroring host decode), and once by round-tripping into `pluginapi.ModelRouteResponse` directly. Both assertions pass per implementer test run.

### 5. Baseline model.route subset correctness — PASS

`handleModelRoute` baseline logic (main.go lines 528-547):

```
if cfg.Enabled &&
    containsString(cfg.Models, req.RequestedModel) &&
    bytes.Contains(req.Body, []byte(`"image_url"`)) &&
    containsString(req.AvailableProviders, cfg.FallbackProvider) {
    resp = {Handled: true, TargetKind: provider, Target: cfg.FallbackProvider, TargetModel: cfg.Fallback}
}
```

This matches the spec's described baseline: `enabled && model in models && body contains image_url && provider available -> Handled provider route; else Handled=false`. The `bytes.Contains` heuristic is a known interim (spec: "deliberate intermediate (replaced by T3's full decide() using detectImage)") — evaluated only for subset correctness, which is right. Test `TestHandleMethod_ModelRouteReturnsValidEnvelope` covers the positive subset path with `AvailableProviders: ["opencode-go"]` matching `FallbackProvider`.

### 6. Zero-mainline-change constraint — PASS

Diff stat confirms only 8 files, all under `image-routing-plugin/`:
```
image-routing-plugin/config.go
image-routing-plugin/config_test.go
image-routing-plugin/detect.go
image-routing-plugin/detect_test.go
image-routing-plugin/go.mod
image-routing-plugin/go.sum
image-routing-plugin/main.go
image-routing-plugin/main_test.go
```
No edits to `internal/`, `sdk/`, `cmd/`, or any pre-existing file. go.mod uses `replace github.com/router-for-me/CLIProxyAPI/v7 => ..` (corrected from brief's `../..` since the plugin sits at repo root).

### 7. Code quality — PASS

- **TDD evidence**: config_test.go (4 tests), main_test.go (3 tests), detect_test.go (12 subtests). Every public behavior has coverage.
- **gofmt/go vet**: implementer reports clean; code inspection shows gofmt-aligned struct literals and consistent import grouping.
- **Dead code**: `containsString` is used by `handleModelRoute`. `defaultConfig`/`currentConfig`/`parseRoutingConfig`/`applyConfig` all used. No unreachable code. The baseline `handleModelRoute` is the known interim (T3 replaces it).
- **English comments**: all comments in English; no non-English comments added.
- **No secrets**: no tokens, keys, or auth material in code or go.sum.
- **Error handling**: `log.Fatal` not used; errors returned/logged via logrus. `errorEnvelope` swallows marshal error (impossible for these types) — acceptable.
- **gjson usage**: pure functions, no side effects; `gjson.GetBytes` on nil/empty body safely returns empty result.

## Findings

### Critical
None.

### Important
None.

### Minor

**M1. `applyConfig` resets to defaults on malformed JSON envelope (config.go:69-81)**
On `reconfigure` with a malformed outer JSON envelope (not invalid YAML inside), `applyConfig` falls through to `configStore.Store(defaultConfig())`, discarding the previous config. The spec only mandates "invalid YAML keeps previous config", so this JSON-envelope case is unspecified. Since the host is trusted to produce valid JSON envelopes, this is unlikely in practice, but a more conservative `return` (keep previous) on JSON unmarshal failure would be safer. Not blocking.

**M2. Test global-state hygiene (config_test.go, main_test.go)**
`TestParseRoutingConfig_InvalidYAMLKeepsPrevious` and `TestHandleMethod_ModelRouteReturnsValidEnvelope` mutate the global `configStore` without restoring prior state. In practice each config-dependent test sets its own config first, so cross-test pollution doesn't bite today. If future tests assume a pristine store, they could break. A `t.Cleanup` reset would harden this. Not blocking.

**M3. No test for format normalization in detectImage (detect_test.go)**
`detectImage` lowercases and trims `sourceFormat`, but all 12 test cases use canonical lowercase strings. Adding cases like `"Chat-Completions"` or `"  claude  "` would lock in the normalization behavior. Coverage gap only; implementation is correct. Not blocking.

## Notes

- T1 deviation from brief (baseline decision instead of `Handled=false` placeholder) is explicitly sanctioned by the review brief: "T1's baseline model.route decision is a deliberate intermediate... evaluate it only for correctness of its subset behavior." The implemented baseline is correct for its subset.
- Wire-format fix commit (66a46799) correctly replaced the brief's snake_case response struct with direct marshaling of `pluginapi.ModelRouteResponse`, and added a round-trip test. This was the right call — verified against `sdk/pluginapi/types.go`.
- go.mod replace path correction (`..` instead of `../..`) is correct for the plugin's location at repo root.
- `gjson` dependency pruned by T1's `go mod tidy` and re-added by T2's; final go.mod/go.sum state is consistent with imports.
