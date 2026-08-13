# Wave w3-build-e2e Review (Task T4: build, README, e2e)

**Reviewer**: Oracle (read-only)
**Base**: 3de28b94 · **Head**: c4e8a1ab
**Commits in range**: c85bfeb8 (README), c4e8a1ab (repair)
**Diff file reviewed**: /tmp/opencode/ir-taskbriefs/w-repair.diff

## Verdict: PASS

One-line summary: README matches the brief exactly (feature, protocols, build command, install/discovery, config fields incl. `enabled: true` and `openai-compatible-` prefix note, Chinese, no secrets); the repair commit's behavior is what the README documents; T4 e2e evidence is internally consistent with the contract acceptance criteria; zero-mainline confirmed (diff touches only `image-routing-plugin/`); disabled-regression claim verified against pluginhost code.

## Per-check results

### Check 1 — README content vs brief: PASS

| Brief requirement | README (diff lines 22-75) | Result |
|---|---|---|
| Feature: route image requests for `models`-listed models to `fallback-provider`/`fallback`; miss = no-op | Line 25 states exactly this, incl. "fallback 通道不可用" → silent skip | PASS |
| Entry protocols: chat-completions `image_url`, responses `input_image`, Claude `image`, Gemini `inlineData`/`fileData` | Lines 27-34 table lists all four with correct fields | PASS |
| Build cmd (CGO, Go 1.26+, `-buildmode=c-shared -o ../plugins/image-routing-v0.1.0.so .`) | Line 41 matches brief line 14 verbatim; "需要 CGO 与 Go 1.26+" matches | PASS |
| Install: drop in `plugins/`, filename = id+version discovery, gitignore note | Line 46: `<id>-v<version>.so` discovery + "`plugins/` 默认被 gitignore" | PASS |
| Config yaml block | Lines 52-61: includes `plugins.enabled: true` (improvement over brief's yaml which omitted it but mentioned it in prose) + per-plugin `enabled: true` | PASS |
| `enabled` must be `true` (host defaults disable; `plugins.enabled` also required) | Lines 65-66: both `plugins.enabled` and `configs.image-routing.enabled` documented as must-be-true | PASS |
| `fallback` target model | Line 67 | PASS |
| `fallback-provider` required, channel must have creds, silent skip when unavailable | Line 68: all three points present | PASS |
| `fallback-provider` openai-compat prefix note | Line 68: "插件自动兼容 openai-compat 通道的 `openai-compatible-` 前缀匹配" — documents the repair commit's behavior | PASS |
| `models`: image-unsupported list, strip thinking suffix, case-insensitive | Line 69: "去除思考后缀(`/think` 等)、大小写不敏感" | PASS |
| Response/usage model = fallback; CGO main build; no-plugin build skips | Lines 73-74 | PASS |
| Restart log expectation `pluginhost: plugin loaded` + `image-routing: config applied` | Line 75 | PASS |
| Language: Chinese (repo area) | All prose Chinese | PASS |
| No secrets | No keys/tokens/credentials; `fallback-provider: opencode-go` is a channel name, not a secret | PASS |
| No dead content | All sections relevant | PASS |

### Check 2 — Repair commit consistency with T4 deliverables: PASS

The repair commit (c4e8a1ab) makes two fixes:
1. **SourceFormat strings**: `chat-completions` → `openai`, `responses` → `openai-response` (matching host `HandlerType()` values). The README's protocol table uses user-facing entry names ("chat-completions", "responses"), not internal format strings, so no conflict.
2. **Provider prefix matching**: `matchAvailableProvider` resolves a configured `opencode-go` to the available key `openai-compatible-opencode-go`. The README line 68 explicitly documents this: "fallback-provider 为通道名,插件自动兼容 openai-compat 通道的 `openai-compatible-` 前缀匹配". The README's config example uses `fallback-provider: opencode-go` (the configured name), which is what users write — the prefix resolution is plugin-internal. Consistent.

The repair does not break T4; it enables the hit scenario to actually fire (which the e2e confirms).

### Check 3 — T4 report evidence vs contract acceptance criteria: PASS

| Contract criterion | T4 report claim | Assessment |
|---|---|---|
| (1) .so builds & loads; log "image-routing: config applied" | Build to /tmp/ir-e2e (7.6 MB); load log + "config applied (enabled=true fallback=mimo-v2.5 fallback-provider=opencode-go models=[deepseek-v4-flash])" | Plausible; log format matches README line 75 and brief line 38 |
| (2) Hit scenario routes (upstream body/request-log model mimo-v2.5 on openai-compatible-opencode-go) | request-log shows upstream body `{"model":"mimo-v2.5",...}` to opencode.ai/.../v1/chat/completions with provider openai-compatible-opencode-go | Matches criterion exactly. The repair's `matchAvailableProvider` resolves `opencode-go` config → `openai-compatible-opencode-go` available key; model swap deepseek-v4-flash → mimo-v2.5 is the routing proof. |
| (2a) Upstream 400 on image content | Report attributes to opencode-go rejecting image content ("Multimodal data is corrupted...") | Correctly attributed. The 400 is upstream behavior post-routing, not a plugin defect. The contract's routing proof is the upstream *request* body model swap, which is satisfied. The plugin's job (reroute) completed successfully before the upstream rejected the image bytes. Internally consistent. |
| (3) Text-only control unchanged | 200 OK, response model deepseek-v4-flash, upstream body model deepseek-v4-flash unchanged | Consistent with `decide()` returning `notHandled` when `detectImage` is false |
| (4) Disabled regression: behavior identical to no-plugin | "no plugin lines in logs (host does not load the plugin when enabled: false); hit body = deepseek-v4-flash, no routing errors" | **Verified against host code** (see focused check below) |
| (5) git zero-mainline check empty | Both `git status --porcelain -- internal/ sdk/ cmd/` and `git diff --stat -- internal/ sdk/ cmd/` empty | Confirmed by diff: only `image-routing-plugin/` files touched |

**Focused check (pluginhost disabled-regression)**: `internal/pluginhost/host.go` `ApplyConfig()` lines 227-234 iterates selected plugin files with `if !item.Enabled { continue }`. When `plugins.configs.image-routing.enabled: false`, the host skips the plugin entirely — no `.so` open, no register call, no `pluginhost: plugin loaded` log, no `config applied` log. The report's "no plugin lines in logs" is literally accurate, and "behavior identical to no-plugin" holds because the plugin is never loaded. Claim verified.

### Check 4 — Zero-mainline: PASS

Diff `Files changed` lists only:
- `image-routing-plugin/README.md` (new)
- `image-routing-plugin/detect.go`
- `image-routing-plugin/detect_test.go`
- `image-routing-plugin/main_test.go`
- `image-routing-plugin/route.go`
- `image-routing-plugin/route_test.go`

No `internal/`, `sdk/`, or `cmd/` paths. The repair commit (c4e8a1ab) was already verified by w1/w2 re-review; it touches only plugin files and does not conflict with T4's README deliverable.

### Check 5 — Code quality: PASS

- README is clear, well-structured (功能 / 支持入口协议 / 构建 / 安装 / 配置 / 说明).
- No dead content; every section maps to a brief requirement.
- No secrets, no placeholder credentials.
- Test changes in the repair are clean: `route_test.go` replaces name-based special-casing (`if tc.name == "disabled config not handled"`) with an explicit `cfg *routingConfig` field — simpler and more general; adds a `non-prefixed provider exact match` case covering the exact-match branch of `matchAvailableProvider`.

## Findings

### Critical
None.

### Important
None.

### Minor
None.

## Notes

- The README's yaml example includes `plugins.enabled: true` at the host level, which the brief's yaml snippet omitted (but stated in prose). This is a positive improvement — the example is directly copy-pasteable and correct.
- The e2e's upstream-400 on the hit scenario is expected and correctly framed: the contract asks for routing proof via the upstream *request* body model, not a successful upstream response. The plugin rerouted correctly; the upstream's rejection of image bytes is orthogonal to the plugin's behavior. The report is internally consistent on this point.
- The disabled-regression claim was the one factual assertion that could not be confirmed from the diff alone; the focused host.go inspection confirms it is accurate.
