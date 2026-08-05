package main

import (
	"strings"

	"github.com/tidwall/gjson"
)

// detectImage reports whether the client request body contains image content
// for the given source protocol format. The format is the host's handler
// HandlerType() string (e.g. "openai" for /v1/chat/completions and
// "openai-response" for /v1/responses).
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
