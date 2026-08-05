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
			name:         "chat-completions image_url",
			format:       "openai",
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:         "chat-completions plain string content",
			format:       "openai",
			body:         `{"messages":[{"role":"user","content":"hi"}]}`,
			wantDetected: false,
		},
		{
			name:         "chat-completions text-only blocks",
			format:       "openai",
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			wantDetected: false,
		},
		{
			name:         "chat-completions tool result image",
			format:       "openai",
			body:         `{"messages":[{"role":"tool","tool_call_id":"t1","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:         "responses input_image in content",
			format:       "openai-response",
			body:         `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`,
			wantDetected: true,
		},
		{
			name:         "responses text only",
			format:       "openai-response",
			body:         `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			wantDetected: false,
		},
		{
			name:         "claude image block",
			format:       "claude",
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:         "claude text only",
			format:       "claude",
			body:         `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			wantDetected: false,
		},
		{
			name:         "gemini inlineData",
			format:       "gemini",
			body:         `{"contents":[{"role":"user","parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`,
			wantDetected: true,
		},
		{
			name:         "gemini fileData",
			format:       "gemini",
			body:         `{"contents":[{"role":"user","parts":[{"fileData":{"fileUri":"gs://bucket/a.png","mimeType":"image/png"}}]}]}`,
			wantDetected: true,
		},
		{
			name:         "gemini text only",
			format:       "gemini",
			body:         `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
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
