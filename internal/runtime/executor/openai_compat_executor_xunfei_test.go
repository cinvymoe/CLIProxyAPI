package executor

import (
	"context"
	"testing"
	"time"
)

func TestIsXunfeiRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		expected bool
	}{
		{
			name:     "empty body",
			body:     []byte{},
			expected: false,
		},
		{
			name:     "no code field",
			body:     []byte(`{"error":{"message":"some error"}}`),
			expected: false,
		},
		{
			name:     "xunfei 10012 nested error",
			body:     []byte(`{"error":{"code":10012,"message":"EngineInternalError:The system is busy, please try again later."}}`),
			expected: true,
		},
		{
			name:     "xunfei 10012 flat error with sid",
			body:     []byte(`{"code":10012,"message":"EngineInternalError:The system is busy, please try again later.","sid":"cht000be954@dx19d95a2001eb958700"}`),
			expected: true,
		},
		{
			name:     "xunfei 10012 actual format with msg and Sid",
			body:     []byte(`{"code":10012,"msg":"EngineInternalError:The system is busy, please try again later.","Sid":"cht000ba7f9@dx19d99825f12b992700","timeStamp":"11:35:51.86"}`),
			expected: true,
		},
		{
			name:     "xunfei 10012 flat error",
			body:     []byte(`{"code":10012,"message":"EngineInternalError:The system is busy, please try again later."}`),
			expected: true,
		},
		{
			name:     "xunfei other error code nested",
			body:     []byte(`{"error":{"code":10013,"message":"some other error"}}`),
			expected: false,
		},
		{
			name:     "xunfei other error code flat",
			body:     []byte(`{"code":10013,"message":"some other error"}`),
			expected: false,
		},
		{
			name:     "invalid json",
			body:     []byte(`not json`),
			expected: false,
		},
		{
			name:     "openai style error",
			body:     []byte(`{"error":{"message":"invalid request","type":"invalid_request_error"}}`),
			expected: false,
		},
		{
			name:     "code zero nested",
			body:     []byte(`{"error":{"code":0,"message":"ok"}}`),
			expected: false,
		},
		{
			name:     "code zero flat",
			body:     []byte(`{"code":0,"message":"ok"}`),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isXunfeiRetryableError(tt.body)
			if result != tt.expected {
				t.Errorf("isXunfeiRetryableError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestXunfeiRetryConstants(t *testing.T) {
	if xunfeiMaxRetries != 3 {
		t.Errorf("xunfeiMaxRetries = %d, want 3", xunfeiMaxRetries)
	}
	if len(xunfeiRetryWait) != 3 {
		t.Errorf("len(xunfeiRetryWait) = %d, want 3", len(xunfeiRetryWait))
	}
	if xunfeiRetryWait[0] != 2*time.Second {
		t.Errorf("xunfeiRetryWait[0] = %v, want 2s", xunfeiRetryWait[0])
	}
}

func TestSleepWithContext(t *testing.T) {
	ctx := context.Background()
	err := sleepWithContext(ctx, 1*time.Millisecond)
	if err != nil {
		t.Errorf("sleepWithContext returned unexpected error: %v", err)
	}
}

func TestSleepWithContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleepWithContext(ctx, 5*time.Second)
	if err == nil {
		t.Error("sleepWithContext should return error when context is cancelled")
	}
}