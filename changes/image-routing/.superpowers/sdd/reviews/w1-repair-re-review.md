# w1 Re-Review (Repair): skeleton-detect

- Base: 3de28b94
- Head: c4e8a1ab
- Wave: w1 (detect)
- Revision: execution-plan rev 2 (prior receipts invalidated)
- Reviewer: Oracle
- Verdict: **PASS**

## Scope

w1 covers the four detection requirements from
`changes/image-routing/specs/image-routing/spec.md`:
- chat-completions detection SHALL trigger on `SourceFormat == "openai"`
  (messages[].content[] `type=="image_url"`, including tool messages; string
  content = no match)
- responses SHALL trigger on `SourceFormat == "openai-response"`
  (input[]/content[] `type=="input_image"`)
- claude `image` blocks
- gemini `inlineData`/`fileData`
- unknown format -> false

## Host Contract Verification (focused check outside diff)

The repair's correctness hinges on the actual `HandlerType()` strings the host
passes as `SourceFormat`. Verified against the host (not in diff):

- `internal/constant/constant.go:20` -> `OpenAI = "openai"`
- `internal/constant/constant.go:23` -> `OpenaiResponse = "openai-response"`
- `sdk/api/handlers/openai/openai_handlers.go:47-49` -> `OpenAIAPIHandler.HandlerType()` returns `OpenAI` ("openai")
- `sdk/api/handlers/openai/openai_responses_handlers.go:345-347` -> `OpenAIResponsesAPIHandler.HandlerType()` returns `OpenaiResponse` ("openai-response")

The plugin's `detectImage` switch cases (`"openai"`, `"openai-response"`,
`"claude"`, `"gemini"`) match the host's actual constants exactly.

## Claim Verification

| Claim | Result | Evidence |
| --- | --- | --- |
| detect.go switch matches "openai"/"openai-response"/"claude"/"gemini" | PASS | detect.go:15-22 |
| Comment updated to reference host HandlerType() | PASS | detect.go:10-12 |
| detect_test labels use "openai"/"openai-response" | PASS | detect_test.go:14,20,26,32,38,44 |
| chat-completions: messages[].content[] type=="image_url" | PASS | detect.go:30-39 |
| chat-completions: tool messages covered | PASS | detect.go:31 iterates all messages incl. role=="tool"; test "chat-completions tool result image" |
| chat-completions: string content = no match | PASS | gjson `.Array()` returns empty for string; test "chat-completions plain string content" |
| responses: input[]/content[] type=="input_image" | PASS | detect.go:43-54 (both top-level and nested content) |
| claude: type=="image" | PASS | detect.go:58-66 |
| gemini: inlineData/fileData | PASS | detect.go:70-78; tests cover both |
| unknown format -> false | PASS | detect.go:23-24 default; test "unknown format" |

## Spec Scenario Coverage

All four detection requirements' scenarios are covered by `TestDetectImage`
subtests with the corrected `SourceFormat` strings. The `strings.ToLower(
strings.TrimSpace(sourceFormat))` guard is a benign leniency (host passes
lowercase already); it does not downgrade correctness.

## Build/Test Verification

- `gofmt -l .` -> clean (no files listed)
- `go vet ./...` -> clean (no output)
- `go test -v ./...` -> all pass (12 detect subtests + 3 main + 9 route + 3 empty-config)

## Findings

None. No Critical, Important, or Minor findings for the w1 scope. The detect
changes are minimal, correct, and fully aligned with both the spec and the
host's actual `HandlerType()` constants.

## Verdict

**PASS** - The detect switch now matches the host's real `SourceFormat` strings
("openai" and "openai-response"), all four detection paths are preserved, tests
are updated to the corrected strings, and the build/test/gofmt/vet suite is
green. The e2e routing blocker (root cause #1) is resolved for the detect side.
