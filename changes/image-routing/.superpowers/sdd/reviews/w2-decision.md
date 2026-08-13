# Wave w2-decision Review — T3 routing decision & model.route wiring

**Reviewer**: Oracle (read-only)
**Base**: a47a41d7
**Head**: 3de28b94
**Branch**: dev
**Planned wave**: w2-decision (T3)
**Diff source**: /tmp/opencode/ir-taskbriefs/w2.diff

## Verdict: PASS

One-line summary: decide() implements every spec scenario with correct ordering and case-insensitivity, handleModelRoute is wired through okEnvelope preserving the tagless camelCase wire format, zero mainline changes, tests cover the full decision matrix.

## Files in diff (all under image-routing-plugin/, zero mainline)
- go.mod / go.sum — added `github.com/tidwall/sjson v1.2.5 // indirect` (transitive dep of internal/thinking, now imported by route.go).
- main.go — handleModelRoute reduced to unmarshal + okEnvelope(decide(...)); T1 baseline body, containsString helper, and unused bytes import removed.
- route.go (new) — decide() + containsFold().
- route_test.go (new) — TestDecide_RouteMatrix (8 cases) + TestDecide_EmptyConfigFieldsNotHandled (3 cases).

## Focused cross-checks performed (outside diff)
- `internal/thinking/suffix.go:23` — `ParseSuffix(model string) SuffixResult`; `SuffixResult.ModelName` field name confirmed (suffix.go:27,39-43). Usage in route.go is correct.
- `sdk/pluginapi/types.go:525` — `ModelRouteRequest{SourceFormat, RequestedModel, Stream, Body, AvailableProviders, ...}` matches test/route.go usage.
- `sdk/pluginapi/types.go:566-578` — `ModelRouteResponse{Handled, TargetKind, Target, TargetModel, Reason}` confirmed **NO json tags** → Go default camelCase wire format. `ModelRouteTargetProvider = "provider"` confirmed (types.go:559).
- `image-routing-plugin/config.go:12-33` — `routingConfig{Enabled, Fallback, FallbackProvider, Models}` and `currentConfig() routingConfig` signatures match route.go consumption.
- `image-routing-plugin/detect.go:11` — `detectImage(body []byte, sourceFormat string) bool` signature matches route.go call.
- `image-routing-plugin/go.mod:1` — module path `github.com/router-for-me/CLIProxyAPI/v7/image-routing-plugin` shares the `github.com/router-for-me/CLIProxyAPI/v7/` prefix, so importing `internal/thinking` is legal under Go's internal-package rule.
- `image-routing-plugin/main_test.go:45-90` — pre-existing T1 handleMethod test still passes under T3 wiring (hit scenario produces identical Target/TargetModel/TargetKind; wire format preserved).

## Per-check results

### 1. decide() decision scenarios — PASS
Spec "改路决策" + "流式与非流式一致性" scenarios mapped to code (route.go:10-29):

| Scenario | Code path | Result |
|---|---|---|
| Hit | all guards pass, containsFold(models,base)=true, detectImage=true, containsFold(providers,fp)=true → Handled=true with Target/TargetModel/Reason | ✓ |
| Thinking suffix | `ParseSuffix("deepseek-v4-flash(high)").ModelName` = "deepseek-v4-flash" → matches | ✓ |
| Case-insensitive | `strings.EqualFold` in containsFold (route.go:34) | ✓ |
| Model outside list | containsFold false → early notHandled | ✓ |
| Text-only body | detectImage false → early notHandled | ✓ |
| Provider unavailable | containsFold(AvailableProviders, fp) false → early notHandled | ✓ |
| Disabled | `!cfg.Enabled` short-circuits at route.go:13 | ✓ |
| Empty config fields | `cfg.Fallback == "" || cfg.FallbackProvider == "" || len(cfg.Models) == 0` short-circuits at route.go:13 | ✓ |
| Stream ignored | decide() never reads `req.Stream`; "streaming request hits identically" test asserts identical Handled=true | ✓ |

Guard order matches spec: config sanity → suffix-strip + model match → image detect → provider availability. Correct.

### 2. Suffix stripping — PASS
- Field name `ModelName` confirmed against suffix.go (not `Base`/`Model`).
- Base-empty fallback (`if base == "" { base = strings.TrimSpace(req.RequestedModel) }`) follows brief verbatim. Practically defensive only — when ParseSuffix yields empty ModelName, the fallback also yields a non-matching string — but no behavioral defect.
- Case-insensitivity via `strings.EqualFold` in containsFold for both models list and providers list.

### 3. detectImage integration — PASS
`detectImage(req.Body, req.SourceFormat)` called at route.go:19 after model-list match, before provider-availability check. Signature matches detect.go:11. Gate position correct (image detection only runs when model is eligible, avoiding unnecessary body scans).

### 4. handleModelRoute wiring — PASS
main.go:146-151:
```go
func handleModelRoute(request []byte) ([]byte, error) {
    var req pluginapi.ModelRouteRequest
    if err := json.Unmarshal(request, &req); err != nil {
        return nil, err
    }
    return okEnvelope(decide(req, currentConfig()))
}
```
- Unmarshal into `pluginapi.ModelRouteRequest` ✓
- `okEnvelope(decide(req, currentConfig()))` ✓
- T1 baseline body removed (the `cfg.Enabled && containsString && bytes.Contains(...)` block) ✓
- `containsString` helper removed ✓
- Unused `bytes` import removed ✓
- No dead helpers left in main.go.

### 5. Wire format preserved — PASS
`okEnvelope` marshals the tagless `pluginapi.ModelRouteResponse` directly → Go default JSON keys `Handled`/`TargetKind`/`Target`/`TargetModel`/`Reason` (camelCase). Pre-existing main_test.go:68-89 decodes the envelope result with explicit camelCase tags AND into the host's `pluginapi.ModelRouteResponse` (round-trip) — both succeed, confirming the wire contract.

### 6. Zero mainline change — PASS
Diff confined to `image-routing-plugin/`. No file outside that directory touched. sjson addition is to the plugin's own go.mod/go.sum, not the mainline module.

### 7. Code quality — PASS (with minor notes)
- TDD evidence: route_test.go covers all 8 spec scenarios + 3 empty-config scenarios; tests assert Handled/TargetKind/Target/TargetModel behavior (not tautologies).
- gofmt/vet: report claims clean; diff formatting visually consistent.
- English comments only (route.go:10-13, main.go:141-145).
- No secrets in diff.
- Reason field set with stable diagnostic string.

## Findings

### Critical
None.

### Important
None.

### Minor
- **M1 (test brittleness)**: route_test.go:295 uses `if tc.name == "disabled config not handled"` to special-case the disabled config. If that case is renamed, the test would silently stop exercising the disabled path while still passing. Prefer an explicit `disabled bool` (or `mutate func(*routingConfig)`) field on the case struct. Pure test-quality nit; behavior is correct today.
- **M2 (Reason not asserted)**: decide() sets `Reason: "image request routed to configured fallback model"` on hits, but no test asserts it. Spec marks Reason optional/diagnostic, so not required, but a one-line assertion on the hit cases would lock the diagnostic contract.
- **M3 (base-empty fallback is effectively dead)**: The `if base == "" { base = strings.TrimSpace(req.RequestedModel) }` branch (route.go:18-20) cannot change outcomes — when ParseSuffix returns empty ModelName, the fallback also yields a string that won't match a real models list. It matches the brief verbatim, so not a deviation, but it's defensive code that doesn't earn its keep. Acceptable as-is.
- **M4 (sjson indirect dep)**: go.mod/go.sum gained `tidwall/sjson v1.2.5 // indirect`. Mechanically correct (internal/thinking transitively requires it now that route.go imports it). Not a mainline change; no action needed. Noted only for completeness.

## Notes for next wave
- Wave w2-decision closes the decision contract. Downstream waves should not alter decide()'s guard order or the tagless-marshaling contract in handleModelRoute.
- If a future wave adds streaming-specific behavior, the spec's "stream must not influence the decision" invariant must be preserved — decide() currently never reads `req.Stream`, which is the correct shape.

## Outcome
Gate satisfied. w2-decision passes. Proceed to next wave.
