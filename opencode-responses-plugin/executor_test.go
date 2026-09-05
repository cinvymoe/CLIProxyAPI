package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// syntheticLargeSSE creates a realistic upstream SSE byte stream where the
// first event (response.created) contains a large tools array (~targetSize).
// It returns the concatenated byte stream and the individual event strings for
// reference.
func syntheticLargeSSE(targetSize int) []byte {
	// Build a tools entry and replicate it to reach targetSize.
	tool := `{"type":"function","name":"tool_fn","description":"desc","parameters":{"type":"object","properties":{"p":{"type":"string"}}}}`
	// Approx size per tool.
	per := len(tool) + 1
	count := targetSize / per
	if count < 1 {
		count = 1
	}
	var tools []string
	for i := 0; i < count; i++ {
		tools = append(tools, tool)
	}
	toolsJSON := "[" + strings.Join(tools, ",") + "]"
	createdPayload := `{"type":"response.created","response":{"id":"resp_test_large","model":"muse-spark-1.2-contributor","created_at":1234567890,"tools":` + toolsJSON + `,"output":[]}}`
	deltaPayload := `{"type":"response.output_text.delta","delta":"hello world"}`
	completedPayload := `{"type":"response.completed","response":{"id":"resp_test_large","status":"completed"}}`
	// Done sentinel as data: [DONE]
	var buf bytes.Buffer
	buf.WriteString("data: ")
	buf.WriteString(createdPayload)
	buf.WriteString("\n\n")
	buf.WriteString("data: ")
	buf.WriteString(deltaPayload)
	buf.WriteString("\n\n")
	buf.WriteString("data: ")
	buf.WriteString(completedPayload)
	buf.WriteString("\n\n")
	buf.WriteString("data: [DONE]\n\n")
	return buf.Bytes()
}

func TestSSEReassembler_ByteFaithfulReassembly(t *testing.T) {
	stream := syntheticLargeSSE(20 * 1024) // ~20KB first event
	// Test multiple split strategies.
	chunkSizes := []int{4096, 12951, 12952, 500, 1, 8192, 1000}
	for _, sz := range chunkSizes {
		var pending []byte
		var emitted [][]byte
		// Feed in slices of sz.
		for i := 0; i < len(stream); i += sz {
			end := i + sz
			if end > len(stream) {
				end = len(stream)
			}
			chunk := stream[i:end]
			pending = append(pending, chunk...)
			evts := extractCompleteSSEEvents(&pending)
			emitted = append(emitted, evts...)
		}
		// Flush remaining tail (simulate EOF flush as runUpstreamStream does)
		if len(pending) > 0 {
			// Remaining should be empty if stream ended with \n\n, but flush anyway.
			remaining := make([]byte, len(pending))
			copy(remaining, pending)
			if findSSEEventEnd(remaining) == 0 {
				remaining = append(remaining, '\n', '\n')
			}
			emitted = append(emitted, remaining)
			pending = pending[:0]
		}
		// (a) every emitted chunk ends at an event boundary
		for i, ev := range emitted {
			if findSSEEventEnd(ev) == 0 {
				// Allow last flush case where we added terminator, but original events must be bounded.
				// For emitted produced by extractCompleteSSEEvents they always have terminator.
				// Re-check via suffix.
				if !bytes.HasSuffix(ev, []byte("\n\n")) && !bytes.HasSuffix(ev, []byte("\r\n\r\n")) && !bytes.HasSuffix(ev, []byte("\r\r")) {
					t.Fatalf("chunk %d does not end at event boundary: %q tail=%q len=%d", i, ev[:min(100, len(ev))], ev[max(0, len(ev)-10):], len(ev))
				}
			}
			// also ensure findSSEEventEnd returns full length (i.e., exactly one event per emitted)
			if l := findSSEEventEnd(ev); l != 0 && l != len(ev) {
				// If emitted contains exactly one event, its boundary should equal its length.
				// For safety, allow multi-event? But extractCompleteSSEEvents emits one event per entry.
				t.Fatalf("chunk %d boundary mismatch: findSSEEventEnd=%d len=%d", i, l, len(ev))
			}
		}
		// (b) concatenating emitted chunks equals original (byte-faithful, modulo our flush terminator addition)
		// For this stream, original already ends with \n\n, so flush adds nothing extra.
		joined := bytes.Join(emitted, nil)
		if !bytes.Equal(joined, stream) {
			t.Fatalf("byte-faithful reassembly failed for size %d: got %d bytes want %d bytes diff at %d", sz, len(joined), len(stream), firstDiff(joined, stream))
		}
		// (c) each event's data payload json-parses
		for i, ev := range emitted {
			payload, ok := sseDataPayload(ev)
			if !ok {
				t.Fatalf("chunk %d missing data: payload", i)
			}
			payload = bytes.TrimSpace(payload)
			if bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			if !json.Valid(payload) {
				t.Fatalf("chunk %d data payload invalid JSON: %q", i, string(payload[:min(200, len(payload))]))
			}
			// Also gjson valid
			if !gjson.ValidBytes(payload) {
				t.Fatalf("chunk %d gjson invalid", i)
			}
		}
	}
}

// sseDataPayload extracts the data: line payload from an SSE event bytes, for test.

func sseDataPayload(event []byte) ([]byte, bool) {
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("data:")) {
			return bytes.TrimSpace(trimmed[len("data:"):]), true
		}
	}
	return nil, false
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func TestSSEReassembler_Split12951Regression(t *testing.T) {
	// Specific regression: upstream data line length 12951 torn mid-line must be reassembled.
	stream := syntheticLargeSSE(20000)
	// Find a position around 12951 where the first event's data line is split.
	// Instead of arbitrary 4096, we specifically split at 12951 as observed in production.
	splitAt := 12951
	if splitAt >= len(stream) {
		splitAt = len(stream) / 2
	}
	chunk1 := stream[:splitAt]
	chunk2 := stream[splitAt:]
	var pending []byte
	var emitted [][]byte
	pending = append(pending, chunk1...)
	evts := extractCompleteSSEEvents(&pending)
	emitted = append(emitted, evts...)
	// After first split, pending should hold the torn tail (partial event) and no event yet emitted for the large frame if torn.
	// For a 20KB first event (~20k + "data: " + "\n\n" = ~20k+8), split at 12951 is mid-data-line, so zero events yet.
	if len(emitted) != 0 {
		// If large event was <12951, it would be complete, but our synthetic is ~20KB so mid-line -> 0.
		// Allow either 0 or 1 depending on size, but ensure no torn emit.
		t.Logf("emitted %d events after split at 12951 (pending %d bytes)", len(emitted), len(pending))
	}
	pending = append(pending, chunk2...)
	evts = extractCompleteSSEEvents(&pending)
	emitted = append(emitted, evts...)
	// Also flush if pending remains (should be empty now)
	if len(pending) > 0 {
		t.Fatalf("pending not empty after full stream")
	}
	joined := bytes.Join(emitted, nil)
	if !bytes.Equal(joined, stream) {
		t.Fatalf("12951 split reassembly failed")
	}
	// Verify the large frame's JSON parses.
	for _, ev := range emitted {
		payload, ok := sseDataPayload(ev)
		if !ok {
			continue
		}
		payload = bytes.TrimSpace(payload)
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if !json.Valid(payload) {
			t.Fatalf("torn payload invalid JSON after 12951 split: %q", string(payload[:min(200, len(payload))]))
		}
	}
}

func TestSSEReassembler_MaxSizeSafety(t *testing.T) {
	// No terminator, large buffer >1MiB should be emitted anyway.
	large := bytes.Repeat([]byte("x"), maxSSEEventSize+100)
	// Prefix with "data: " to look like SSE but no terminator.
	raw := append([]byte("data: "), large...)
	var pending []byte
	pending = append(pending, raw...)
	evts := extractCompleteSSEEvents(&pending)
	if len(evts) != 1 {
		t.Fatalf("expected 1 oversized event, got %d", len(evts))
	}
	if !bytes.Equal(evts[0], raw) {
		t.Fatalf("oversized emit not byte-equal")
	}
	if len(pending) != 0 {
		t.Fatalf("pending should be empty after oversize flush")
	}
}

func TestTranslateAfterReassembly_12951(t *testing.T) {
	stream := syntheticLargeSSE(20 * 1024)
	// Simulate chat path: feed reassembled complete events through translate.
	var pending []byte
	pending = append(pending, stream...)
	// Extract but then feed via reassembler chunking at 12951.
	// Actually simulate incremental feeding as in runUpstreamStream chat branch.
	var reassembledComplete []byte
	var tmpPending []byte
	for i := 0; i < len(stream); i += 12951 {
		end := i + 12951
		if end > len(stream) {
			end = len(stream)
		}
		tmpPending = append(tmpPending, stream[i:end]...)
		evts := extractCompleteSSEEvents(&tmpPending)
		for _, ev := range evts {
			reassembledComplete = append(reassembledComplete, ev...)
		}
	}
	if len(tmpPending) > 0 {
		reassembledComplete = append(reassembledComplete, tmpPending...)
	}
	if !bytes.Equal(reassembledComplete, stream) {
		t.Fatalf("chat reassembly mismatch")
	}
	// Now translate reassembledComplete: should produce valid chat chunks without JSON error.
	state := &chatStreamState{model: "muse-spark-1.2-contributor"}
	out := translateResponsesStreamToChat(reassembledComplete, state, "muse-spark-1.2-contributor")
	if len(out) == 0 {
		t.Fatalf("expected translated chunks")
	}
	for _, chunk := range out {
		if bytes.Equal(chunk, []byte("[DONE]")) {
			continue
		}
		if !gjson.ValidBytes(chunk) {
			t.Fatalf("translated chunk invalid JSON: %q", string(chunk[:min(200, len(chunk))]))
		}
	}
	// Verify no torn JSON: translate on torn halves without reassembly would have failed.
	// Simulate torn payload directly (mid-line) fed to translate: should not produce valid delta for torn large frame (it would be filtered / fallback).
	torn := stream[:12951]
	state2 := &chatStreamState{model: "muse-spark-1.2-contributor"}
	outTorn := translateResponsesStreamToChat(torn, state2, "muse-spark-1.2-contributor")
	// Torn large created event's JSON is incomplete, so translate should not yield delta content from it (but also not crash).
	// Ensure it does not produce a spurious "hello" etc? At least ensure it doesn't panic and out is empty or without invalid JSON.
	for _, c := range outTorn {
		if !gjson.ValidBytes(c) && !bytes.Equal(c, []byte("[DONE]")) {
			t.Fatalf("torn translate produced invalid JSON")
		}
	}
}

func TestExtractCompleteSSEEvents_MultipleEventsPerChunk(t *testing.T) {
	stream := syntheticLargeSSE(1024)
	var pending []byte
	pending = append(pending, stream...)
	evts := extractCompleteSSEEvents(&pending)
	if len(evts) != 4 { // created, delta, completed, DONE
		t.Fatalf("expected 4 events, got %d", len(evts))
	}
	if len(pending) != 0 {
		t.Fatalf("pending not empty")
	}
	// Each event ends with \n\n
	for _, ev := range evts {
		if !bytes.HasSuffix(ev, []byte("\n\n")) {
			t.Fatalf("event missing suffix")
		}
	}
}

func TestChatToResponses_ToolsFlattened(t *testing.T) {
	payload := []byte(`{
		"model": "muse-spark-1.2-contributor",
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [
			{"type": "function", "function": {"name": "get_time", "description": "Get current time", "parameters": {"type": "object", "properties": {}}}},
			{"type": "function", "name": "already_flat", "parameters": {"type": "object"}}
		],
		"tool_choice": {"type": "function", "function": {"name": "get_time"}},
		"reasoning_effort": "high"
	}`)
	out := chatToResponses(payload, "", false)
	if !gjson.ValidBytes(out) {
		t.Fatalf("output not valid JSON: %s", string(out))
	}
	if got := gjson.GetBytes(out, "tools.0.name").String(); got != "get_time" {
		t.Fatalf("tools[0].name = %q, want get_time; body=%s", got, string(out))
	}
	if gjson.GetBytes(out, "tools.0.function").Exists() {
		t.Fatalf("tools[0] still has nested function object: %s", string(out))
	}
	if got := gjson.GetBytes(out, "tools.0.parameters.type").String(); got != "object" {
		t.Fatalf("tools[0].parameters lost: %s", string(out))
	}
	if got := gjson.GetBytes(out, "tools.1.name").String(); got != "already_flat" {
		t.Fatalf("flat tool entry not preserved: %s", string(out))
	}
	if got := gjson.GetBytes(out, "tool_choice.name").String(); got != "get_time" {
		t.Fatalf("tool_choice not flattened: %s", string(out))
	}
	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning_effort not mapped: %s", string(out))
	}
	if gjson.GetBytes(out, "reasoning_effort").Exists() {
		t.Fatalf("reasoning_effort leaked into responses payload: %s", string(out))
	}
}

func TestChatToResponses_ToolChoiceStringPassthrough(t *testing.T) {
	payload := []byte(`{
		"model": "m",
		"messages": [{"role": "user", "content": "hi"}],
		"tools": [{"type": "function", "function": {"name": "f", "parameters": {"type": "object"}}}],
		"tool_choice": "auto"
	}`)
	out := chatToResponses(payload, "", false)
	if got := gjson.GetBytes(out, "tool_choice").String(); got != "auto" {
		t.Fatalf("tool_choice string = %q, want auto; body=%s", got, string(out))
	}
}

func TestResponsesToChatNonStream_FunctionCall(t *testing.T) {
	resp := []byte(`{
		"id": "resp_abc",
		"created_at": 1700000000,
		"model": "muse-spark-1.2-contributor",
		"status": "completed",
		"output": [
			{"type": "reasoning", "encrypted_content": "xxx"},
			{"type": "function_call", "id": "fc_1", "call_id": "call_123", "name": "get_time", "arguments": "{\"tz\":\"utc\"}"}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`)
	chat := responsesToChatNonStream(resp, "")
	if !gjson.ValidBytes(chat) {
		t.Fatalf("output not valid JSON: %s", string(chat))
	}
	if got := gjson.GetBytes(chat, "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls; body=%s", got, string(chat))
	}
	if got := gjson.GetBytes(chat, "choices.0.message.tool_calls.0.id").String(); got != "call_123" {
		t.Fatalf("tool_calls[0].id = %q, want call_123; body=%s", got, string(chat))
	}
	if got := gjson.GetBytes(chat, "choices.0.message.tool_calls.0.function.name").String(); got != "get_time" {
		t.Fatalf("tool_calls[0].function.name = %q; body=%s", got, string(chat))
	}
	if got := gjson.GetBytes(chat, "choices.0.message.tool_calls.0.function.arguments").String(); got != `{"tz":"utc"}` {
		t.Fatalf("tool_calls[0].function.arguments = %q; body=%s", got, string(chat))
	}
	if got := gjson.GetBytes(chat, "usage.prompt_tokens").Int(); got != 10 {
		t.Fatalf("usage not mapped: %s", string(chat))
	}
}

func TestTranslateResponsesStreamToChat_FunctionCall(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_x\",\"model\":\"muse-spark-1.2-contributor\",\"created_at\":1700000000}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_9\",\"name\":\"get_time\",\"arguments\":\"\"}}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\\\"tz\\\":\"}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"\\\"utc\\\"}\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_9\",\"name\":\"get_time\",\"arguments\":\"{\\\"tz\\\":\\\"utc\\\"}\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_x\",\"status\":\"completed\"}}\n\n"
	state := &chatStreamState{model: "muse-spark-1.2-contributor"}
	out := translateResponsesStreamToChat([]byte(stream), state, "muse-spark-1.2-contributor")
	var args strings.Builder
	sawStart := false
	finish := ""
	for _, chunk := range out {
		if bytes.Equal(chunk, []byte("[DONE]")) {
			continue
		}
		if !gjson.ValidBytes(chunk) {
			t.Fatalf("invalid JSON chunk: %q", string(chunk))
		}
		tc := gjson.GetBytes(chunk, "choices.0.delta.tool_calls.0")
		if tc.Exists() {
			if tc.Get("id").String() == "call_9" {
				sawStart = true
				if got := tc.Get("function.name").String(); got != "get_time" {
					t.Fatalf("tool call name = %q; chunk=%s", got, string(chunk))
				}
			}
			args.WriteString(tc.Get("function.arguments").String())
		}
		if fr := gjson.GetBytes(chunk, "choices.0.finish_reason"); fr.Exists() && fr.String() != "" {
			finish = fr.String()
		}
	}
	if !sawStart {
		t.Fatalf("missing tool_calls start chunk with id; out=%v", out)
	}
	if got := args.String(); got != `{"tz":"utc"}` {
		t.Fatalf("streamed arguments = %q, want %s", got, `{"tz":"utc"}`)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", finish)
	}
}

func TestTranslateResponsesStreamToChat_FunctionCallNoDeltas(t *testing.T) {
	stream := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_y\",\"model\":\"m\",\"created_at\":1700000000}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"f\",\"arguments\":\"\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"f\",\"arguments\":\"{\\\"a\\\":1}\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_y\",\"status\":\"completed\"}}\n\n"
	state := &chatStreamState{model: "m"}
	out := translateResponsesStreamToChat([]byte(stream), state, "m")
	var args strings.Builder
	for _, chunk := range out {
		if bytes.Equal(chunk, []byte("[DONE]")) {
			continue
		}
		args.WriteString(gjson.GetBytes(chunk, "choices.0.delta.tool_calls.0.function.arguments").String())
	}
	if got := args.String(); got != `{"a":1}` {
		t.Fatalf("arguments without deltas = %q, want %s", got, `{"a":1}`)
	}
}

func TestChatToResponses_AssistantHistoryWithToolCalls(t *testing.T) {
	payload := []byte(`{
		"model": "muse-spark-1.2-contributor",
		"messages": [
			{"role": "user", "content": "what time is it"},
			{"role": "assistant", "content": "Let me check.", "tool_calls": [{"id": "call_abc", "type": "function", "function": {"name": "get_time", "arguments": "{}"}}]},
			{"role": "tool", "tool_call_id": "call_abc", "content": "13:15"}
		]
	}`)
	out := chatToResponses(payload, "", false)
	if !gjson.ValidBytes(out) {
		t.Fatalf("output not valid JSON: %s", string(out))
	}
	if got := gjson.GetBytes(out, "input.0.content.0.type").String(); got != "input_text" {
		t.Fatalf("user part type = %q, want input_text; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "input.1.content.0.type").String(); got != "output_text" {
		t.Fatalf("assistant part type = %q, want output_text; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "input.2.type").String(); got != "function_call" {
		t.Fatalf("input[2].type = %q, want function_call; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "input.2.call_id").String(); got != "call_abc" {
		t.Fatalf("function_call.call_id = %q; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "input.3.type").String(); got != "function_call_output" {
		t.Fatalf("input[3].type = %q, want function_call_output; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "input.3.call_id").String(); got != "call_abc" {
		t.Fatalf("function_call_output.call_id = %q; body=%s", got, string(out))
	}
}

func TestChatToResponses_ThinkingSuffixStripped(t *testing.T) {
	payload := []byte(`{
		"model": "muse-spark-1.2-contributor(high)",
		"messages": [{"role": "user", "content": "hi"}]
	}`)
	out := chatToResponses(payload, "", false)
	if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.2-contributor" {
		t.Fatalf("model = %q, suffix not stripped; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", got, string(out))
	}
	// Explicit reasoning_effort wins over suffix.
	payload2 := []byte(`{
		"model": "muse-spark-1.2-contributor(high)",
		"messages": [{"role": "user", "content": "hi"}],
		"reasoning_effort": "low"
	}`)
	out2 := chatToResponses(payload2, "", false)
	if got := gjson.GetBytes(out2, "reasoning.effort").String(); got != "low" {
		t.Fatalf("reasoning.effort = %q, want low (explicit wins); body=%s", got, string(out2))
	}
}

func TestNormalizeUpstreamModel_Suffix(t *testing.T) {
	payload := []byte(`{"model":"muse-spark-1.2-contributor(max)","input":"hi","stream":true}`)
	out := normalizeUpstreamModel(payload)
	if got := gjson.GetBytes(out, "model").String(); got != "muse-spark-1.2-contributor" {
		t.Fatalf("model = %q; body=%s", got, string(out))
	}
	if got := gjson.GetBytes(out, "reasoning.effort").String(); got != "max" {
		t.Fatalf("reasoning.effort = %q, want max; body=%s", got, string(out))
	}
	// No suffix: payload unchanged.
	plain := []byte(`{"model":"muse-spark-1.2-contributor","input":"hi"}`)
	if got := normalizeUpstreamModel(plain); string(got) != string(plain) {
		t.Fatalf("payload without suffix was modified: %s", string(got))
	}
	// Existing reasoning object is preserved.
	withReasoning := []byte(`{"model":"m(high)","input":"hi","reasoning":{"effort":"low"}}`)
	out3 := normalizeUpstreamModel(withReasoning)
	if got := gjson.GetBytes(out3, "reasoning.effort").String(); got != "low" {
		t.Fatalf("existing reasoning overwritten: %s", string(out3))
	}
}
