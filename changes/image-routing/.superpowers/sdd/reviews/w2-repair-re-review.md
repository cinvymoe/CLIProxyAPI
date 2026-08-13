# w2 Re-Review (Repair): decision

- Base: 3de28b94
- Head: c4e8a1ab
- Wave: w2 (decide)
- Revision: execution-plan rev 2 (prior receipts invalidated)
- Reviewer: Oracle
- Verdict: **PASS**

## Scope

w2 covers the `改路决策` requirement from
`changes/image-routing/specs/image-routing/spec.md`:

Handled=true iff (a) enabled && fallback/fallback-provider/models non-empty;
(b) requested model (suffix-stripped, case-insensitive) in models;
(c) detectImage; (d) fallback-provider available via exact OR
`openai-compatible-` prefix match. Target = the ACTUAL available key;
TargetModel = fallback; else Handled=false. Stream must not influence.

## Host Contract Verification (focused check outside diff)

The repair's correctness hinges on the actual prefix the host adds to
openai-compat channel keys in `AvailableProviders`. Verified against the host
(not in diff):

- `internal/util/provider.go:15` -> `const openAICompatibleProviderPrefix = "openai-compatible-"`
- `internal/util/provider.go:18-27` -> `OpenAICompatibleProviderKey(name)` returns `openAICompatibleProviderPrefix + name` for normal names.

The plugin's local `openAICompatProviderPrefix = "openai-compatible-"`
(route.go:12) matches the host's value exactly. The host const is unexported,
so the plugin cannot import it; the local copy is structurally necessary and
documented with a `// see internal/util/provider.go` comment.

## Claim Verification

| Claim | Result | Evidence |
| --- | --- | --- |
| openAICompatProviderPrefix const added | PASS | route.go:10-12 |
| matchAvailableProvider: exact OR prefix match | PASS | route.go:49-58 |
| matchAvailableProvider: case-insensitive | PASS | uses `strings.EqualFold` (route.go:53) |
| matchAvailableProvider: whitespace-trimmed | PASS | `strings.TrimSpace` on both sides (route.go:50,52) |
| decide returns actual available key as Target | PASS | route.go:33-43 (`Target: target` where `target` is the matched `k`) |
| TargetModel = fallback | PASS | route.go:41 (`TargetModel: cfg.Fallback`) |
| Stream does not influence decision | PASS | `req.Stream` is not referenced anywhere in `decide` |
| Tests: hit cases use prefixed providers, expect prefixed Target | PASS | route_test.go:37-38,42-43,47-48,73-74 |
| Tests: added non-prefixed exact-match case | PASS | route_test.go:65-70 (cfg.FallbackProvider="gemini", available=["gemini"], Target="gemini") |
| Tests: unavailable case uses prefixed xfyun key | PASS | route_test.go:61-64 |
| main_test SourceFormat "openai" | PASS | main_test.go:48 |
| main_test expects prefixed Target | PASS | main_test.go:77,87 |
| gofmt/vet/test all green | PASS | verified: gofmt -l clean, go vet clean, all tests pass |

## Spec Scenario Coverage

| Spec Scenario | Test | Result |
| --- | --- | --- |
| 命中改路 (hit) | "hit routes to fallback provider and model" | PASS |
| 思考后缀模型命中 | "thinking suffix model hits" | PASS |
| 列表外模型含图不处理 | "model outside list not handled" | PASS |
| 列表内模型不含图不处理 | "text-only body not handled" | PASS |
| fallback-provider 不可用不处理 | "fallback provider unavailable not handled" | PASS |
| 大小写不敏感匹配 | "case-insensitive model match" | PASS |
| 插件未启用 | "disabled config not handled" | PASS |
| 流式请求命中 | "streaming request hits identically" | PASS |
| (extra) non-prefixed exact match | "non-prefixed provider exact match" | PASS |

## Decision Logic Walk-Through

`decide()` (route.go:18-44) order of checks:
1. Config validation: enabled + non-empty fields -> early `notHandled` ✅ (matches spec cond. (a))
2. Suffix-strip + case-insensitive model match -> early `notHandled` ✅ (matches spec cond. (b))
3. `detectImage` -> early `notHandled` ✅ (matches spec cond. (c))
4. `matchAvailableProvider` -> early `notHandled` if "" ✅ (matches spec cond. (d))
5. Returns `Handled=true, TargetKind=provider, Target=<actual key>, TargetModel=fallback` ✅

`matchAvailableProvider` (route.go:49-58) checks both:
- exact: `strings.EqualFold(k, cfg)` ✅
- prefixed: `strings.EqualFold(k, openAICompatProviderPrefix+cfg)` ✅
Returns the trimmed available key `k` (the actual key in `AvailableProviders`).
Returns `""` when no match. ✅

Edge cases considered:
- configured already prefixed (e.g. "openai-compatible-opencode-go") + available prefixed -> exact match handles it. ✅
- configured non-prefixed + available non-prefixed (e.g. "gemini") -> exact match. ✅
- configured non-prefixed + available prefixed -> prefix match. ✅
- configured "" -> guarded by config validation (FallbackProvider == "" early return); matchAvailableProvider never called with empty. ✅
- available empty/nil -> loop doesn't execute, returns "". ✅

## Build/Test Verification

- `gofmt -l .` -> clean
- `go vet ./...` -> clean
- `go test -v ./...` -> all pass (TestDecide_RouteMatrix 9 subtests, TestDecide_EmptyConfigFieldsNotHandled 3 subtests, TestHandleMethod_ModelRouteReturnsValidEnvelope, etc.)

## Findings

### Minor (test hygiene) - route_test.go:104

`TestDecide_EmptyConfigFieldsNotHandled` still uses `SourceFormat: "chat-completions"`
which is no longer a valid format string after the repair. The test passes only
because the config-field validation (`cfg.Fallback == ""`, etc.) returns early
before `detectImage` is called, so the stale format is never evaluated. The
behavior under test is correct, but the stale format string is inconsistent
with the rest of the suite (which was updated to "openai"). Recommend updating
to `"openai"` for consistency. Not a correctness bug; does not block the wave.

### Minor (const duplication risk) - route.go:12

The plugin defines its own `openAICompatProviderPrefix = "openai-compatible-"`
duplicating the host's unexported `openAICompatibleProviderPrefix` in
`internal/util/provider.go:15`. The duplication is structurally necessary
(host const is unexported; plugin is a c-shared `main` package) and is
documented with a `// see internal/util/provider.go` comment. Risk: if the host
changes the prefix, the plugin silently breaks. Mitigation: the prefix is part
of the user-visible channel naming convention (appears in configs/logs), so
changing it would be a breaking change for the host itself. Low risk;
acceptable. Not a correctness bug; does not block the wave.

## Verdict

**PASS** - The decide path now performs exact OR `openai-compatible-` prefix
matching (case-insensitive, trimmed) via `matchAvailableProvider`, returns the
actual available key as `Target`, keeps `Stream` out of the decision, and all
spec scenarios are covered by passing tests including the new non-prefixed
exact-match case. The e2e routing blocker (root cause #2) is resolved for the
decide side. The two Minor findings are test-hygiene/structural notes that do
not affect correctness.
