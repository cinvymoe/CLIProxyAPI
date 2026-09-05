package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// defaultUserAgent matches the opencode-go provider headers in config.yaml.
const defaultUserAgent = "opencode/1.17.9 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14"

// rpcExecutorRequest mirrors the host-side rpcExecutorRequest wire shape
// (see internal/pluginhost/rpc_schema.go).
type rpcExecutorRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// hostHTTPRequest mirrors the host-side rpcHostHTTPRequest wire shape
// (see internal/pluginhost/host_callbacks.go).
type hostHTTPRequest struct {
	HostCallbackID string              `json:"host_callback_id,omitempty"`
	Method         string              `json:"method,omitempty"`
	URL            string              `json:"url,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           []byte              `json:"body,omitempty"`
}

// hostHTTPStreamResponse mirrors the host-side rpcHostHTTPStreamResponse.
type hostHTTPStreamResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StreamID   string              `json:"stream_id,omitempty"`
}

type hostHTTPStreamReadRequest struct {
	StreamID string `json:"stream_id"`
}

// hostHTTPStreamReadResponse mirrors the host-side rpcHostHTTPStreamReadResponse.
type hostHTTPStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

type hostHTTPStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
}

type rpcStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type rpcStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

// inboundHeaderDenylist lists inbound request headers that must not be
// forwarded upstream because the plugin sets them explicitly or they are
// hop-by-hop headers.
var inboundHeaderDenylist = map[string]struct{}{
	"authorization":   {},
	"content-type":    {},
	"content-length":  {},
	"host":            {},
	"connection":      {},
	"accept-encoding": {},
	"user-agent":      {},
}

// upstreamURL builds the upstream endpoint from the configured base URL.
// Alt "responses/compact" selects the compact upstream variant.
func upstreamURL(cfg pluginConfig, alt string) string {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if strings.TrimSpace(alt) == "responses/compact" {
		return base + "/responses/compact"
	}
	return base + "/responses"
}

// buildUpstreamHeaders forwards inbound headers except denylisted ones, then
// applies the upstream auth/content headers.
func buildUpstreamHeaders(cfg pluginConfig, inbound http.Header) http.Header {
	out := make(http.Header, len(inbound)+3)
	for key, values := range inbound {
		if _, blocked := inboundHeaderDenylist[strings.ToLower(strings.TrimSpace(key))]; blocked {
			continue
		}
		for _, value := range values {
			out.Add(key, value)
		}
	}
	out.Set("Content-Type", "application/json")
	out.Set("Authorization", "Bearer "+cfg.APIKey)
	out.Set("User-Agent", defaultUserAgent)
	return out
}

// ensureStreamFlag forces the payload "stream" field to match the execution
// mode so the upstream /responses endpoint answers in the expected shape.
func ensureStreamFlag(payload []byte, stream bool) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}
	current := gjson.GetBytes(payload, "stream")
	if current.Exists() && current.Bool() == stream {
		return payload
	}
	out, errSet := sjson.SetBytes(payload, "stream", stream)
	if errSet != nil {
		return payload
	}
	return out
}

// isChatRequest reports whether the executor request originated as a chat
// completions request (Format/SourceFormat == "openai" or payload has messages).
func isChatRequest(req rpcExecutorRequest) bool {
	if strings.EqualFold(strings.TrimSpace(req.SourceFormat), openAIChatFormat) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(req.Format), openAIChatFormat) {
		return true
	}
	if len(req.Payload) > 0 && gjson.ValidBytes(req.Payload) {
		if v := gjson.GetBytes(req.Payload, "messages"); v.Exists() && v.IsArray() {
			return true
		}
		// Explicit chat style may also have "messages" as a non-array fallback (should be array but handle).
		if gjson.GetBytes(req.Payload, "messages").Exists() && !gjson.GetBytes(req.Payload, "input").Exists() {
			// If payload looks like chat (has messages but not input), treat as chat.
			// The check above already covers array case; this catches edge.
			return true
		}
	}
	return false
}

// chatToResponses converts an OpenAI chat completions payload to an OpenAI
// Responses payload suitable for the upstream /responses endpoint.
func chatToResponses(payload []byte, modelFallback string, stream bool) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return ensureStreamFlag(payload, stream)
	}
	root := gjson.ParseBytes(payload)
	model := strings.TrimSpace(root.Get("model").String())
	if model == "" {
		model = strings.TrimSpace(modelFallback)
	}
	// Strip thinking suffix (e.g. "model(high)") from the upstream model name.
	model, suffixEffort := stripThinkingSuffix(model)
	// Start from empty object and copy relevant fields.
	out := []byte(`{}`)
	out, _ = sjson.SetBytes(out, "model", model)
	out, _ = sjson.SetBytes(out, "stream", stream)
	if v := root.Get("temperature"); v.Exists() {
		out, _ = sjson.SetBytes(out, "temperature", v.Value())
	}
	if v := root.Get("top_p"); v.Exists() {
		out, _ = sjson.SetBytes(out, "top_p", v.Value())
	}
	// Map max tokens variants to max_output_tokens.
	if v := root.Get("max_output_tokens"); v.Exists() {
		out, _ = sjson.SetBytes(out, "max_output_tokens", v.Value())
	} else if v := root.Get("max_tokens"); v.Exists() {
		out, _ = sjson.SetBytes(out, "max_output_tokens", v.Value())
	} else if v := root.Get("max_completion_tokens"); v.Exists() {
		out, _ = sjson.SetBytes(out, "max_output_tokens", v.Value())
	}
	if v := root.Get("seed"); v.Exists() {
		out, _ = sjson.SetBytes(out, "seed", v.Value())
	}
	if v := root.Get("tools"); v.Exists() && v.IsArray() && len(v.Array()) > 0 {
		out, _ = sjson.SetRawBytes(out, "tools", convertChatTools(v.Raw))
		if v2 := root.Get("tool_choice"); v2.Exists() {
			out, _ = sjson.SetRawBytes(out, "tool_choice", convertChatToolChoice(v2.Raw))
		}
		if v2 := root.Get("parallel_tool_calls"); v2.Exists() {
			out, _ = sjson.SetBytes(out, "parallel_tool_calls", v2.Value())
		}
	}
	// Map chat reasoning fields to the Responses "reasoning" object.
	if v := root.Get("reasoning"); v.Exists() {
		out, _ = sjson.SetRawBytes(out, "reasoning", []byte(v.Raw))
	} else if v := root.Get("reasoning_effort"); v.Exists() && strings.TrimSpace(v.String()) != "" {
		out, _ = sjson.SetBytes(out, "reasoning.effort", strings.TrimSpace(v.String()))
	} else if suffixEffort != "" {
		out, _ = sjson.SetBytes(out, "reasoning.effort", suffixEffort)
	}
	// Preserve instructions if present directly (rare for chat).
	if v := root.Get("instructions"); v.Exists() {
		out, _ = sjson.SetBytes(out, "instructions", v.String())
	}
	// Convert messages to input array.
	if msgs := root.Get("messages"); msgs.Exists() && msgs.IsArray() {
		var inputItems [][]byte
		for _, msg := range msgs.Array() {
			role := strings.TrimSpace(msg.Get("role").String())
			if role == "" {
				role = "user"
			}
			contentVal := msg.Get("content")
			// Tool output messages: role == "tool"
			if strings.EqualFold(role, "tool") {
				toolCallID := strings.TrimSpace(msg.Get("tool_call_id").String())
				var output string
				if contentVal.Type == gjson.String {
					output = contentVal.String()
				} else if contentVal.IsArray() {
					var sb strings.Builder
					for _, p := range contentVal.Array() {
						if txt := p.Get("text").String(); txt != "" {
							sb.WriteString(txt)
						} else if p.Type == gjson.String {
							sb.WriteString(p.String())
						}
					}
					output = sb.String()
					if output == "" {
						output = contentVal.Raw
					}
				} else if contentVal.Exists() {
					output = contentVal.String()
					if output == "" {
						output = contentVal.Raw
					}
				}
				fco := []byte(`{"type":"function_call_output","call_id":"","output":""}`)
				fco, _ = sjson.SetBytes(fco, "call_id", toolCallID)
				fco, _ = sjson.SetBytes(fco, "output", output)
				inputItems = append(inputItems, fco)
				continue
			}
			// Check for assistant tool_calls (function calls emitted as separate items).
			if tcs := msg.Get("tool_calls"); tcs.Exists() && tcs.IsArray() && len(tcs.Array()) > 0 {
				var contentParts [][]byte
				if contentVal.Type == gjson.String {
					if txt := contentVal.String(); txt != "" {
						contentParts = append(contentParts, makeTextPart(textPartType(role), txt))
					}
				} else if contentVal.IsArray() {
					for _, partItem := range contentVal.Array() {
						t := strings.TrimSpace(partItem.Get("type").String())
						if t == "text" || t == "input_text" || t == "output_text" {
							txt := partItem.Get("text").String()
							if txt == "" {
								continue
							}
							contentParts = append(contentParts, makeTextPart(textPartType(role), txt))
						} else if t == "image_url" {
							url := strings.TrimSpace(partItem.Get("image_url.url").String())
							if url == "" {
								url = strings.TrimSpace(partItem.Get("image_url").String())
							}
							if url == "" {
								continue
							}
							p := []byte(`{"type":"input_image","image_url":""}`)
							p, _ = sjson.SetBytes(p, "image_url", url)
							contentParts = append(contentParts, p)
						}
					}
				}
				hasText := len(contentParts) > 0
				if hasText {
					item := []byte(`{"role":"","content":[]}`)
					item, _ = sjson.SetBytes(item, "role", role)
					item = setContentParts(item, contentParts)
					inputItems = append(inputItems, item)
				}
				// Emit each tool_call as function_call item.
				for _, tc := range tcs.Array() {
					callID := strings.TrimSpace(tc.Get("id").String())
					name := strings.TrimSpace(tc.Get("function.name").String())
					args := tc.Get("function.arguments").String()
					if args == "" && tc.Get("function.arguments").Exists() {
						args = tc.Get("function.arguments").Raw
					}
					if args == "" {
						args = "{}"
					}
					fc := []byte(`{"type":"function_call","call_id":"","name":"","arguments":""}`)
					fc, _ = sjson.SetBytes(fc, "call_id", callID)
					fc, _ = sjson.SetBytes(fc, "name", name)
					fc, _ = sjson.SetBytes(fc, "arguments", args)
					inputItems = append(inputItems, fc)
				}
				continue
			}
			// Normal message conversion.
			var contentParts [][]byte
			if contentVal.Type == gjson.String {
				txt := contentVal.String()
				contentParts = append(contentParts, makeTextPart(textPartType(role), txt))
			} else if contentVal.IsArray() {
				for _, partItem := range contentVal.Array() {
					t := strings.TrimSpace(partItem.Get("type").String())
					switch t {
					case "text", "input_text", "output_text":
						txt := partItem.Get("text").String()
						if txt == "" {
							continue
						}
						contentParts = append(contentParts, makeTextPart(textPartType(role), txt))
					case "image_url":
						url := strings.TrimSpace(partItem.Get("image_url.url").String())
						if url == "" {
							url = strings.TrimSpace(partItem.Get("image_url").String())
						}
						if url == "" {
							continue
						}
						p := []byte(`{"type":"input_image","image_url":""}`)
						p, _ = sjson.SetBytes(p, "image_url", url)
						contentParts = append(contentParts, p)
					case "":
						// May be text without type.
						if txt := partItem.Get("text").String(); txt != "" {
							contentParts = append(contentParts, makeTextPart(textPartType(role), txt))
						}
					default:
						if txt := partItem.Get("text").String(); txt != "" {
							contentParts = append(contentParts, makeTextPart(textPartType(role), txt))
						}
					}
				}
			} else if contentVal.Exists() && contentVal.Type == gjson.Null {
				// keep empty
			} else if !contentVal.Exists() {
				// no content, keep empty
			}
			item := []byte(`{"role":"","content":[]}`)
			item, _ = sjson.SetBytes(item, "role", role)
			if len(contentParts) > 0 {
				item = setContentParts(item, contentParts)
			}
			inputItems = append(inputItems, item)
		}
		if len(inputItems) > 0 {
			out, _ = sjson.SetRawBytes(out, "input", joinRawArray(inputItems))
		} else {
			out, _ = sjson.SetRawBytes(out, "input", []byte(`[]`))
		}
	} else if root.Get("input").Exists() {
		// Already responses style (should not happen for chat), preserve.
		out, _ = sjson.SetRawBytes(out, "input", []byte(root.Get("input").Raw))
	}
	// Copy any pass-through fields that responses supports and chat may have, if not already set.
	// Ensure we do not include messages any more.
	return out
}

// convertChatTools converts OpenAI chat completions tool definitions
// ({"type":"function","function":{...}}) into the flat Responses API shape
// ({"type":"function","name":...,"description":...,"parameters":...}).
// Entries without a nested "function" object pass through unchanged.
func convertChatTools(raw string) []byte {
	tools := gjson.Parse(raw)
	if !tools.IsArray() {
		return []byte(raw)
	}
	var out [][]byte
	for _, tool := range tools.Array() {
		fn := tool.Get("function")
		if !fn.Exists() || !fn.IsObject() {
			out = append(out, []byte(tool.Raw))
			continue
		}
		flat := []byte(`{"type":"function"}`)
		if t := strings.TrimSpace(tool.Get("type").String()); t != "" {
			flat, _ = sjson.SetBytes(flat, "type", t)
		}
		for _, key := range []string{"name", "description"} {
			if v := fn.Get(key); v.Exists() {
				flat, _ = sjson.SetBytes(flat, key, v.Value())
			}
		}
		if v := fn.Get("parameters"); v.Exists() {
			flat, _ = sjson.SetRawBytes(flat, "parameters", []byte(v.Raw))
		}
		if v := fn.Get("strict"); v.Exists() {
			flat, _ = sjson.SetBytes(flat, "strict", v.Value())
		}
		out = append(out, flat)
	}
	return joinRawArray(out)
}

// convertChatToolChoice converts a chat completions tool_choice value into the
// Responses API shape. String values ("auto", "none", "required") pass through;
// {"type":"function","function":{"name":X}} becomes {"type":"function","name":X}.
func convertChatToolChoice(raw string) []byte {
	tc := gjson.Parse(raw)
	if !tc.IsObject() {
		return []byte(raw)
	}
	fn := tc.Get("function")
	if !fn.Exists() || !fn.IsObject() {
		return []byte(raw)
	}
	flat := []byte(`{"type":"function"}`)
	if t := strings.TrimSpace(tc.Get("type").String()); t != "" {
		flat, _ = sjson.SetBytes(flat, "type", t)
	}
	if v := fn.Get("name"); v.Exists() {
		flat, _ = sjson.SetBytes(flat, "name", v.String())
	}
	return flat
}

// stripThinkingSuffix removes a thinking suffix (e.g. "(high)") from a model
// name and returns the base model name plus the suffix as reasoning effort.
func stripThinkingSuffix(model string) (base, effort string) {
	parsed := thinking.ParseSuffix(strings.TrimSpace(model))
	base = strings.TrimSpace(parsed.ModelName)
	if base == "" {
		base = strings.TrimSpace(model)
	}
	if parsed.HasSuffix {
		effort = strings.TrimSpace(parsed.RawSuffix)
	}
	return base, effort
}

// normalizeUpstreamModel strips any thinking suffix from a Responses payload's
// model field and maps it to reasoning.effort when reasoning is not already set.
func normalizeUpstreamModel(payload []byte) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}
	model := strings.TrimSpace(gjson.GetBytes(payload, "model").String())
	if model == "" {
		return payload
	}
	base, effort := stripThinkingSuffix(model)
	if base == model {
		return payload
	}
	out, errSet := sjson.SetBytes(payload, "model", base)
	if errSet != nil {
		return payload
	}
	if effort != "" && !gjson.GetBytes(payload, "reasoning").Exists() {
		if out2, errReason := sjson.SetBytes(out, "reasoning.effort", effort); errReason == nil {
			out = out2
		}
	}
	return out
}

// textPartType returns the Responses content part type for a message role:
// assistant messages carry "output_text", all other roles carry "input_text".
func textPartType(role string) string {
	if strings.EqualFold(strings.TrimSpace(role), "assistant") {
		return "output_text"
	}
	return "input_text"
}

// makeTextPart builds a Responses text content part of the appropriate type.
func makeTextPart(partType, text string) []byte {
	part := []byte(`{"type":"","text":""}`)
	part, _ = sjson.SetBytes(part, "type", partType)
	part, _ = sjson.SetBytes(part, "text", text)
	return part
}

// setContentParts assigns a content parts array on a message item.
func setContentParts(item []byte, parts [][]byte) []byte {
	var vals []interface{}
	for _, p := range parts {
		vals = append(vals, gjson.ParseBytes(p).Value())
	}
	newItem, _ := sjson.SetBytes(item, "content", vals)
	return newItem
}

func joinRawArray(items [][]byte) []byte {
	if len(items) == 0 {
		return []byte(`[]`)
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(it)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// responsesToChatNonStream converts a non-streaming Responses JSON payload
// to an OpenAI chat completions JSON payload.
func responsesToChatNonStream(respBody []byte, modelFallback string) []byte {
	if len(respBody) == 0 || !gjson.ValidBytes(respBody) {
		return respBody
	}
	root := gjson.ParseBytes(respBody)
	// If it already looks like chat (has choices), return as is.
	if root.Get("choices").Exists() {
		return respBody
	}
	id := strings.TrimSpace(root.Get("id").String())
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	// Convert resp_ prefix to chatcmpl_ if present.
	if strings.HasPrefix(id, "resp_") {
		id = "chatcmpl-" + strings.TrimPrefix(id, "resp_")
	}
	created := root.Get("created_at").Int()
	if created == 0 {
		created = root.Get("created").Int()
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	model := strings.TrimSpace(root.Get("model").String())
	if model == "" {
		model = strings.TrimSpace(modelFallback)
	}
	// Aggregate text from output messages and collect function calls.
	var textBuilder strings.Builder
	var toolCalls [][]byte
	if out := root.Get("output"); out.Exists() && out.IsArray() {
		for _, item := range out.Array() {
			switch strings.TrimSpace(item.Get("type").String()) {
			case "message":
				if content := item.Get("content"); content.IsArray() {
					for _, c := range content.Array() {
						ct := strings.TrimSpace(c.Get("type").String())
						if ct == "output_text" || ct == "text" || ct == "input_text" {
							textBuilder.WriteString(c.Get("text").String())
						}
					}
				} else if content.Type == gjson.String {
					textBuilder.WriteString(content.String())
				}
			case "function_call":
				callID := strings.TrimSpace(item.Get("call_id").String())
				if callID == "" {
					callID = strings.TrimSpace(item.Get("id").String())
				}
				tc := []byte(`{"id":"","type":"function","function":{"name":"","arguments":""}}`)
				tc, _ = sjson.SetBytes(tc, "id", callID)
				tc, _ = sjson.SetBytes(tc, "function.name", item.Get("name").String())
				args := item.Get("arguments").String()
				if args == "" && item.Get("arguments").Exists() {
					args = item.Get("arguments").Raw
				}
				tc, _ = sjson.SetBytes(tc, "function.arguments", args)
				toolCalls = append(toolCalls, tc)
			}
		}
	}
	// Fallback: try direct output_text field.
	if textBuilder.Len() == 0 {
		if t := root.Get("output_text"); t.Exists() {
			if t.IsArray() {
				for _, v := range t.Array() {
					if v.Type == gjson.String {
						textBuilder.WriteString(v.String())
					} else if txt := v.Get("text").String(); txt != "" {
						textBuilder.WriteString(txt)
					}
				}
			} else if t.Type == gjson.String {
				textBuilder.WriteString(t.String())
			}
		}
	}
	text := textBuilder.String()
	// Build chat completion response.
	chat := []byte(`{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"usage":null}`)
	chat, _ = sjson.SetBytes(chat, "id", id)
	chat, _ = sjson.SetBytes(chat, "created", created)
	chat, _ = sjson.SetBytes(chat, "model", model)
	chat, _ = sjson.SetBytes(chat, "choices.0.message.content", text)
	if len(toolCalls) > 0 {
		var tcVals []interface{}
		for _, tc := range toolCalls {
			tcVals = append(tcVals, gjson.ParseBytes(tc).Value())
		}
		chat, _ = sjson.SetBytes(chat, "choices.0.message.tool_calls", tcVals)
	}
	// Determine finish reason.
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	if root.Get("status").String() == "incomplete" {
		if r := root.Get("incomplete_details.reason").String(); r == "max_output_tokens" {
			finishReason = "length"
		} else if r != "" {
			finishReason = "stop"
		}
	}
	chat, _ = sjson.SetBytes(chat, "choices.0.finish_reason", finishReason)
	// Map usage if present.
	if usage := root.Get("usage"); usage.Exists() {
		var prompt, completion, total int64
		if v := usage.Get("input_tokens"); v.Exists() {
			prompt = v.Int()
		} else if v := usage.Get("prompt_tokens"); v.Exists() {
			prompt = v.Int()
		}
		if v := usage.Get("output_tokens"); v.Exists() {
			completion = v.Int()
		} else if v := usage.Get("completion_tokens"); v.Exists() {
			completion = v.Int()
		}
		if v := usage.Get("total_tokens"); v.Exists() {
			total = v.Int()
		} else {
			total = prompt + completion
		}
		if usage.Exists() {
			chat, _ = sjson.SetBytes(chat, "usage.prompt_tokens", prompt)
			chat, _ = sjson.SetBytes(chat, "usage.completion_tokens", completion)
			chat, _ = sjson.SetBytes(chat, "usage.total_tokens", total)
		}
	}
	return chat
}

// chatStreamState holds incremental state for translating a Responses SSE
// stream into chat completion SSE chunks.
type chatStreamState struct {
	responseID string
	created    int64
	model      string
	sentRole   bool
	// toolCallIndexes maps a Responses output_index to the chat tool_calls
	// array index for function calls seen during the stream.
	toolCallIndexes map[string]int
	// toolCallArgsSeen tracks chat tool_calls indexes that already received
	// streamed argument deltas.
	toolCallArgsSeen map[int]bool
	nextToolIndex    int
	sawToolCall      bool
}

// maxSSEEventSize is a safety bound for a single SSE event without a
// terminator. If no "\n\n" / "\r\n\r\n" / "\r\r" boundary is found and the
// buffered event exceeds this size, it is emitted anyway to avoid unbounded
// memory growth. Emitting a truncated event may surface a downstream JSON
// error, but that is preferable to an OOM. Normal responses events are <100KB;
// the pathological large tools array in response.created is ~15KB.
const maxSSEEventSize = 1 << 20 // 1 MiB

// findSSEEventEnd returns the length of the first complete SSE event in buf
// (including its trailing "\n\n" / "\r\n\r\n" / "\r\r"), or 0 if incomplete.
func findSSEEventEnd(buf []byte) int {
	best := 0
	bestLen := 0
	if idx := bytes.Index(buf, []byte("\r\n\r\n")); idx >= 0 {
		best = idx
		bestLen = 4
	}
	if idx := bytes.Index(buf, []byte("\n\n")); idx >= 0 {
		if bestLen == 0 || idx < best {
			best = idx
			bestLen = 2
		}
	}
	if idx := bytes.Index(buf, []byte("\r\r")); idx >= 0 {
		if bestLen == 0 || idx < best {
			best = idx
			bestLen = 2
		}
	}
	if bestLen == 0 {
		return 0
	}
	return best + bestLen
}

// extractCompleteSSEEvents drains complete events from *pending into a slice.
// Each returned event includes its terminating blank line. The pending buffer
// is updated to keep only the incomplete tail. If a single event grows past
// maxSSEEventSize without a terminator, it is emitted as-is.
func extractCompleteSSEEvents(pending *[]byte) [][]byte {
	var out [][]byte
	for len(*pending) > 0 {
		end := findSSEEventEnd(*pending)
		if end > 0 {
			evt := make([]byte, end)
			copy(evt, (*pending)[:end])
			out = append(out, evt)
			*pending = (*pending)[end:]
			continue
		}
		if len(*pending) > maxSSEEventSize {
			evt := make([]byte, len(*pending))
			copy(evt, *pending)
			out = append(out, evt)
			*pending = (*pending)[:0]
		}
		break
	}
	return out
}

// buildChatDeltaChunk creates a chat.completion.chunk with a content delta.
func buildChatDeltaChunk(state *chatStreamState, delta string) []byte {
	id := state.responseID
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	created := state.created
	if created == 0 {
		created = time.Now().Unix()
	}
	model := state.model
	if model == "" {
		model = "muse-spark-1.2-contributor"
	}
	chunk := []byte(`{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"content":""},"finish_reason":null}]}`)
	chunk, _ = sjson.SetBytes(chunk, "id", id)
	chunk, _ = sjson.SetBytes(chunk, "created", created)
	chunk, _ = sjson.SetBytes(chunk, "model", model)
	chunk, _ = sjson.SetBytes(chunk, "choices.0.delta.content", delta)
	return chunk
}

func buildChatRoleChunk(state *chatStreamState) []byte {
	id := state.responseID
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	created := state.created
	if created == 0 {
		created = time.Now().Unix()
	}
	model := state.model
	if model == "" {
		model = "muse-spark-1.2-contributor"
	}
	chunk := []byte(`{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)
	chunk, _ = sjson.SetBytes(chunk, "id", id)
	chunk, _ = sjson.SetBytes(chunk, "created", created)
	chunk, _ = sjson.SetBytes(chunk, "model", model)
	return chunk
}

// buildChatToolCallChunk creates a chat.completion.chunk carrying a tool_calls
// delta. For the first delta of a call pass id and name; subsequent argument
// deltas pass only argsDelta.
func buildChatToolCallChunk(state *chatStreamState, index int, id, name, argsDelta string) []byte {
	id0 := state.responseID
	if id0 == "" {
		id0 = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	created := state.created
	if created == 0 {
		created = time.Now().Unix()
	}
	model := state.model
	if model == "" {
		model = "muse-spark-1.2-contributor"
	}
	fn := map[string]interface{}{}
	if name != "" {
		fn["name"] = name
	}
	fn["arguments"] = argsDelta
	tc := map[string]interface{}{"index": index, "type": "function", "function": fn}
	if id != "" {
		tc["id"] = id
	}
	chunk := []byte(`{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":null}]}`)
	chunk, _ = sjson.SetBytes(chunk, "id", id0)
	chunk, _ = sjson.SetBytes(chunk, "created", created)
	chunk, _ = sjson.SetBytes(chunk, "model", model)
	chunk, _ = sjson.SetBytes(chunk, "choices.0.delta.tool_calls", []interface{}{tc})
	return chunk
}

// toolCallIndexFor returns the chat tool_calls index assigned to a Responses
// output_index, allocating a new one on first sight.
func toolCallIndexFor(state *chatStreamState, outputIndex string) int {
	if state.toolCallIndexes == nil {
		state.toolCallIndexes = make(map[string]int)
	}
	if idx, ok := state.toolCallIndexes[outputIndex]; ok {
		return idx
	}
	idx := state.nextToolIndex
	state.toolCallIndexes[outputIndex] = idx
	state.nextToolIndex++
	return idx
}

func buildChatFinishChunk(state *chatStreamState, finishReason string) []byte {
	id := state.responseID
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	created := state.created
	if created == 0 {
		created = time.Now().Unix()
	}
	model := state.model
	if model == "" {
		model = "muse-spark-1.2-contributor"
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	chunk := []byte(`{"id":"","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":""}]}`)
	chunk, _ = sjson.SetBytes(chunk, "id", id)
	chunk, _ = sjson.SetBytes(chunk, "created", created)
	chunk, _ = sjson.SetBytes(chunk, "model", model)
	chunk, _ = sjson.SetBytes(chunk, "choices.0.finish_reason", finishReason)
	return chunk
}

// translateResponsesStreamToChat translates a single upstream Responses SSE
// payload chunk into zero or more chat SSE chunks. It updates state in place.
func translateResponsesStreamToChat(payload []byte, state *chatStreamState, modelFallback string) [][]byte {
	text := string(payload)
	// Fast path: if payload does not look like SSE, try to extract delta via gjson.
	if !strings.Contains(text, "data:") && gjson.ValidBytes(payload) {
		// Might be a single JSON event without SSE framing.
		typ := gjson.GetBytes(payload, "type").String()
		if typ == "response.output_text.delta" {
			delta := gjson.GetBytes(payload, "delta").String()
			if delta != "" {
				if state.responseID == "" {
					state.responseID = "chatcmpl-" + fmt.Sprintf("%d", time.Now().UnixNano())
					if modelFallback != "" {
						state.model = modelFallback
					}
				}
				var out [][]byte
				if !state.sentRole {
					roleChunk := buildChatRoleChunk(state)
					out = append(out, roleChunk)
					state.sentRole = true
				}
				deltaChunk := buildChatDeltaChunk(state, delta)
				out = append(out, deltaChunk)
				return out
			}
		}
		if typ == "response.created" {
			if v := gjson.GetBytes(payload, "response.id"); v.Exists() && v.String() != "" {
				id := v.String()
				if strings.HasPrefix(id, "resp_") {
					id = "chatcmpl-" + strings.TrimPrefix(id, "resp_")
				}
				state.responseID = id
			}
			if v := gjson.GetBytes(payload, "response.created_at"); v.Exists() && v.Int() != 0 {
				state.created = v.Int()
			}
			if v := gjson.GetBytes(payload, "response.model"); v.Exists() && v.String() != "" {
				state.model = v.String()
			} else if modelFallback != "" && state.model == "" {
				state.model = modelFallback
			}
			return nil
		}
		return nil
	}
	var out [][]byte
	// Split by \n\n to get individual SSE events.
	events := strings.Split(text, "\n\n")
	for _, event := range events {
		event = strings.TrimSpace(event)
		if event == "" {
			continue
		}
		lines := strings.Split(event, "\n")
		var dataParts []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				dataParts = append(dataParts, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		for _, data := range dataParts {
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				out = append(out, []byte("[DONE]"))
				continue
			}
			if !gjson.Valid(data) {
				continue
			}
			typ := gjson.Get(data, "type").String()
			switch typ {
			case "response.created":
				if v := gjson.Get(data, "response.id"); v.Exists() && v.String() != "" {
					id := v.String()
					if strings.HasPrefix(id, "resp_") {
						id = "chatcmpl-" + strings.TrimPrefix(id, "resp_")
					}
					state.responseID = id
				}
				if v := gjson.Get(data, "response.created_at"); v.Exists() && v.Int() != 0 {
					state.created = v.Int()
				}
				if v := gjson.Get(data, "response.model"); v.Exists() && v.String() != "" {
					state.model = v.String()
				} else if modelFallback != "" && state.model == "" {
					state.model = modelFallback
				}
			case "response.output_text.delta":
				delta := gjson.Get(data, "delta").String()
				if delta == "" {
					continue
				}
				if !state.sentRole {
					roleChunk := buildChatRoleChunk(state)
					out = append(out, roleChunk)
					state.sentRole = true
				}
				deltaChunk := buildChatDeltaChunk(state, delta)
				out = append(out, deltaChunk)
			case "response.output_item.added":
				item := gjson.Get(data, "item")
				if item.Get("type").String() != "function_call" {
					continue
				}
				state.sawToolCall = true
				idx := toolCallIndexFor(state, gjson.Get(data, "output_index").String())
				callID := strings.TrimSpace(item.Get("call_id").String())
				if callID == "" {
					callID = strings.TrimSpace(item.Get("id").String())
				}
				if !state.sentRole {
					roleChunk := buildChatRoleChunk(state)
					out = append(out, roleChunk)
					state.sentRole = true
				}
				out = append(out, buildChatToolCallChunk(state, idx, callID, item.Get("name").String(), ""))
			case "response.function_call_arguments.delta":
				state.sawToolCall = true
				idx := toolCallIndexFor(state, gjson.Get(data, "output_index").String())
				delta := gjson.Get(data, "delta").String()
				if delta == "" {
					continue
				}
				if state.toolCallArgsSeen == nil {
					state.toolCallArgsSeen = make(map[int]bool)
				}
				state.toolCallArgsSeen[idx] = true
				if !state.sentRole {
					roleChunk := buildChatRoleChunk(state)
					out = append(out, roleChunk)
					state.sentRole = true
				}
				out = append(out, buildChatToolCallChunk(state, idx, "", "", delta))
			case "response.output_item.done":
				item := gjson.Get(data, "item")
				if item.Get("type").String() != "function_call" {
					continue
				}
				state.sawToolCall = true
				idx := toolCallIndexFor(state, gjson.Get(data, "output_index").String())
				// Emit full arguments only when no deltas were streamed for this call.
				if state.toolCallArgsSeen[idx] {
					continue
				}
				args := item.Get("arguments").String()
				if args == "" && item.Get("arguments").Exists() {
					args = item.Get("arguments").Raw
				}
				if args == "" {
					continue
				}
				if !state.sentRole {
					roleChunk := buildChatRoleChunk(state)
					out = append(out, roleChunk)
					state.sentRole = true
				}
				out = append(out, buildChatToolCallChunk(state, idx, "", "", args))
			case "response.completed", "response.incomplete":
				reason := "stop"
				if state.sawToolCall {
					reason = "tool_calls"
				}
				if typ == "response.incomplete" {
					if r := gjson.Get(data, "response.incomplete_details.reason").String(); r == "max_output_tokens" {
						reason = "length"
					}
				}
				finishChunk := buildChatFinishChunk(state, reason)
				out = append(out, finishChunk)
				out = append(out, []byte("[DONE]"))
			case "response.output_text.done", "response.content_part.done":
				// ignore, finish handled via completed
			default:
				// For other events, ignore.
			}
		}
	}
	// Also handle case where payload contains a single delta without proper SSE split (e.g., chunk contains one event line)
	if len(out) == 0 && strings.Contains(text, "response.output_text.delta") {
		// Fallback: try to extract all delta occurrences via gjson scan
		// Attempt to find delta string via simple extraction.
		if delta := gjson.Get(text, "delta").String(); delta != "" {
			if !state.sentRole {
				roleChunk := buildChatRoleChunk(state)
				out = append(out, roleChunk)
				state.sentRole = true
			}
			deltaChunk := buildChatDeltaChunk(state, delta)
			out = append(out, deltaChunk)
		}
	}
	return out
}

// handleExecute implements executor.execute: a blocking upstream POST to the
// Responses endpoint via the host HTTP bridge.
func handleExecute(request []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cfg := currentConfig()
	if errPreflight := preflight(cfg); errPreflight != nil {
		return errorEnvelope("executor_error", errPreflight.Error()), nil
	}
	isChat := isChatRequest(req)
	payload := req.Payload
	if isChat {
		payload = chatToResponses(payload, req.Model, false)
	} else {
		payload = normalizeUpstreamModel(payload)
	}
	payload = ensureStreamFlag(payload, false)
	resp, errDo := hostHTTPDo(req.HostCallbackID, upstreamURL(cfg, req.Alt), buildUpstreamHeaders(cfg, req.Headers), payload)
	if errDo != nil {
		return errorEnvelope("executor_error", errDo.Error()), nil
	}
	if resp.StatusCode >= 400 {
		return errorEnvelope("executor_error", fmt.Sprintf("upstream status %d: %s", resp.StatusCode, snippet(resp.Body))), nil
	}
	if isChat {
		chatBody := responsesToChatNonStream(resp.Body, req.Model)
		return okEnvelope(pluginapi.ExecutorResponse{Payload: chatBody, Headers: resp.Headers})
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: resp.Body, Headers: resp.Headers})
}

// handleExecuteStream implements executor.execute_stream: it returns
// immediately and streams upstream SSE chunks back through the host plugin
// stream bridge (host.stream.emit / host.stream.close).
func handleExecuteStream(request []byte) ([]byte, error) {
	var req rpcExecutorRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	streamID := strings.TrimSpace(req.StreamID)
	if streamID == "" {
		return errorEnvelope("executor_error", "stream_id is required for executor.execute_stream"), nil
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				closePluginStream(streamID, fmt.Sprintf("stream panic: %v", recovered))
			}
		}()
		if errRun := runUpstreamStream(req, streamID); errRun != nil {
			closePluginStream(streamID, errRun.Error())
			return
		}
		closePluginStream(streamID, "")
	}()
	return okEnvelope(map[string]any{
		"headers": http.Header{"Content-Type": []string{"text/event-stream"}},
	})
}

// runUpstreamStream opens the upstream stream via host.http.do_stream and
// forwards every chunk to the plugin stream bridge. It is chunk-safe: upstream
// read boundaries may land mid-line (e.g., a 15KB tools array split at 12951
// bytes). For both chat (translated) and passthrough (openai-response) paths,
// bytes are buffered until a complete SSE event boundary ("\n\n", "\r\n\r\n",
// "\r\r") is present. Partial events are never emitted; the downstream writer
// then receives well-formed events and does not append a spurious terminator
// mid-line.
func runUpstreamStream(req rpcExecutorRequest, pluginStreamID string) error {
	cfg := currentConfig()
	if errPreflight := preflight(cfg); errPreflight != nil {
		return errPreflight
	}
	isChat := isChatRequest(req)
	payload := req.Payload
	if isChat {
		payload = chatToResponses(payload, req.Model, true)
	} else {
		payload = normalizeUpstreamModel(payload)
	}
	payload = ensureStreamFlag(payload, true)
	raw, errCall := callHost(pluginabi.MethodHostHTTPDoStream, hostHTTPRequest{
		HostCallbackID: req.HostCallbackID,
		Method:         http.MethodPost,
		URL:            upstreamURL(cfg, req.Alt),
		Headers:        map[string][]string(buildUpstreamHeaders(cfg, req.Headers)),
		Body:           payload,
	})
	if errCall != nil {
		return errCall
	}
	var streamResp hostHTTPStreamResponse
	if errDecode := json.Unmarshal(raw, &streamResp); errDecode != nil {
		return fmt.Errorf("decode host http stream response: %w", errDecode)
	}
	if strings.TrimSpace(streamResp.StreamID) == "" {
		return fmt.Errorf("host http stream: empty stream_id")
	}
	defer func() { _ = closeHostHTTPStream(streamResp.StreamID) }()

	if streamResp.StatusCode >= 400 {
		return fmt.Errorf("upstream status %d: %s", streamResp.StatusCode, snippet(drainHostHTTPStream(streamResp.StreamID)))
	}
	// State for chat stream translation.
	var chatState chatStreamState
	if isChat {
		chatState.model = strings.TrimSpace(req.Model)
		if chatState.model == "" {
			if m := gjson.GetBytes(req.Payload, "model").String(); strings.TrimSpace(m) != "" {
				chatState.model = strings.TrimSpace(m)
			}
		}
	}
	// SSE reassembly buffer and DONE dedupe.
	var pending []byte
	emittedDone := false
	// helper to check if a payload is exactly "[DONE]" (chat) or an SSE
	// event whose data line is "[DONE]" (passthrough).
	isDONEPayload := func(p []byte) bool {
		t := bytes.TrimSpace(p)
		if bytes.Equal(t, []byte("[DONE]")) {
			return true
		}
		// passthrough events contain "data: [DONE]" line
		if bytes.Contains(t, []byte("[DONE]")) {
			// Verify it is a data line with [DONE] payload.
			for _, line := range bytes.Split(t, []byte("\n")) {
				line = bytes.TrimSpace(bytes.TrimRight(line, "\r"))
				if bytes.HasPrefix(line, []byte("data:")) {
					if bytes.Equal(bytes.TrimSpace(line[len("data:"):]), []byte("[DONE]")) {
						return true
					}
				}
			}
		}
		return false
	}
	markDoneIfNeeded := func(p []byte) {
		if isDONEPayload(p) {
			emittedDone = true
		}
	}
	for {
		chunk, errRead := readHostHTTPStream(streamResp.StreamID)
		if errRead != nil {
			return errRead
		}
		if chunk.Error != "" {
			return fmt.Errorf("upstream stream error: %s", chunk.Error)
		}
		if len(chunk.Payload) > 0 {
			pending = append(pending, chunk.Payload...)
			if isChat {
				events := extractCompleteSSEEvents(&pending)
				if len(events) > 0 {
					complete := bytes.Join(events, nil)
					translated := translateResponsesStreamToChat(complete, &chatState, chatState.model)
					for _, tr := range translated {
						if isDONEPayload(tr) {
							if emittedDone {
								continue
							}
							emittedDone = true
						}
						if errEmit := emitPluginStreamChunk(pluginStreamID, tr); errEmit != nil {
							return errEmit
						}
						markDoneIfNeeded(tr)
					}
				} else if chunk.Done {
					// No complete event yet but upstream signaled Done; flush pending as best effort.
					if len(pending) > 0 {
						translated := translateResponsesStreamToChat(pending, &chatState, chatState.model)
						pending = pending[:0]
						for _, tr := range translated {
							if isDONEPayload(tr) {
								if emittedDone {
									continue
								}
								emittedDone = true
							}
							if errEmit := emitPluginStreamChunk(pluginStreamID, tr); errEmit != nil {
								return errEmit
							}
							markDoneIfNeeded(tr)
						}
					}
				}
			} else {
				// Passthrough (openai-response): emit only complete SSE events verbatim.
				events := extractCompleteSSEEvents(&pending)
				for _, ev := range events {
					if isDONEPayload(ev) {
						if emittedDone {
							continue
						}
						emittedDone = true
					}
					if errEmit := emitPluginStreamChunk(pluginStreamID, ev); errEmit != nil {
						return errEmit
					}
					markDoneIfNeeded(ev)
				}
				if chunk.Done && len(pending) > 0 {
					// Flush remaining tail as a final event (best effort) so the large
					// tools frame is not lost. Ensure it ends with a blank line.
					remaining := make([]byte, len(pending))
					copy(remaining, pending)
					pending = pending[:0]
					if findSSEEventEnd(remaining) == 0 {
						// Append terminator so downstream sees a complete event.
						if bytes.HasSuffix(remaining, []byte("\n")) {
							remaining = append(remaining, '\n')
						} else if bytes.HasSuffix(remaining, []byte("\r")) {
							remaining = append(remaining, '\n', '\n')
						} else {
							remaining = append(remaining, '\n', '\n')
						}
					}
					if isDONEPayload(remaining) && emittedDone {
						// skip duplicate DONE
					} else {
						if isDONEPayload(remaining) {
							emittedDone = true
						}
						_ = emitPluginStreamChunk(pluginStreamID, remaining)
					}
				}
			}
		}
		if chunk.Done {
			// Ensure chat streams end with exactly one [DONE].
			if isChat {
				// Flush any leftover pending that was not yet a complete event.
				if len(pending) > 0 {
					translated := translateResponsesStreamToChat(pending, &chatState, chatState.model)
					pending = pending[:0]
					for _, tr := range translated {
						if isDONEPayload(tr) {
							if emittedDone {
								continue
							}
							emittedDone = true
						}
						_ = emitPluginStreamChunk(pluginStreamID, tr)
						markDoneIfNeeded(tr)
					}
				}
				if chatState.sentRole && !emittedDone {
					_ = emitPluginStreamChunk(pluginStreamID, []byte("[DONE]"))
					emittedDone = true
				}
			} else {
				// For passthrough, if stream ended without a terminal event and we have no pending,
				// rely on upstream terminal event; do not synthesize.
				if len(pending) > 0 {
					remaining := make([]byte, len(pending))
					copy(remaining, pending)
					pending = pending[:0]
					_ = emitPluginStreamChunk(pluginStreamID, remaining)
				}
			}
			return nil
		}
	}
}

// preflight validates that the plugin may execute right now.
func preflight(cfg pluginConfig) error {
	if !cfg.Enabled {
		return fmt.Errorf("opencode-responses plugin is disabled")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("opencode-responses api-key is not configured")
	}
	return nil
}

// hostHTTPDo performs a blocking upstream POST through the host HTTP bridge.
func hostHTTPDo(callbackID, url string, headers http.Header, body []byte) (pluginapi.HTTPResponse, error) {
	raw, errCall := callHost(pluginabi.MethodHostHTTPDo, hostHTTPRequest{
		HostCallbackID: callbackID,
		Method:         http.MethodPost,
		URL:            url,
		Headers:        map[string][]string(headers),
		Body:           body,
	})
	if errCall != nil {
		return pluginapi.HTTPResponse{}, errCall
	}
	var resp pluginapi.HTTPResponse
	if errDecode := json.Unmarshal(raw, &resp); errDecode != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host http response: %w", errDecode)
	}
	return resp, nil
}

// readHostHTTPStream reads the next upstream stream chunk from the host.
func readHostHTTPStream(streamID string) (hostHTTPStreamReadResponse, error) {
	raw, errCall := callHost(pluginabi.MethodHostHTTPStreamRead, hostHTTPStreamReadRequest{StreamID: streamID})
	if errCall != nil {
		return hostHTTPStreamReadResponse{}, errCall
	}
	var chunk hostHTTPStreamReadResponse
	if errDecode := json.Unmarshal(raw, &chunk); errDecode != nil {
		return hostHTTPStreamReadResponse{}, fmt.Errorf("decode host http stream chunk: %w", errDecode)
	}
	return chunk, nil
}

// drainHostHTTPStream reads an upstream error stream to completion and
// returns the aggregated body for diagnostics.
func drainHostHTTPStream(streamID string) []byte {
	var body []byte
	for {
		chunk, errRead := readHostHTTPStream(streamID)
		if errRead != nil {
			return body
		}
		body = append(body, chunk.Payload...)
		if chunk.Done || chunk.Error != "" {
			if len(body) == 0 && chunk.Error != "" {
				return []byte(chunk.Error)
			}
			return body
		}
	}
}

func closeHostHTTPStream(streamID string) error {
	if strings.TrimSpace(streamID) == "" {
		return nil
	}
	_, errCall := callHost(pluginabi.MethodHostHTTPStreamClose, hostHTTPStreamCloseRequest{StreamID: streamID})
	return errCall
}

func emitPluginStreamChunk(streamID string, payload []byte) error {
	if strings.TrimSpace(streamID) == "" {
		return fmt.Errorf("plugin stream id is required")
	}
	_, errCall := callHost(pluginabi.MethodHostStreamEmit, rpcStreamEmitRequest{
		StreamID: streamID,
		Payload:  payload,
	})
	return errCall
}

func closePluginStream(streamID, errMsg string) {
	if strings.TrimSpace(streamID) == "" {
		return
	}
	_, _ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{
		StreamID: streamID,
		Error:    strings.TrimSpace(errMsg),
	})
}

// snippet truncates an upstream error body for inclusion in error messages.
func snippet(body []byte) string {
	const maxLen = 512
	s := strings.TrimSpace(string(body))
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
