package executor

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestClassifyXunfeiError(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		expected xunfeiErrorClass
	}{
		{
			name:     "empty body",
			body:     []byte{},
			expected: xunfeiErrNone,
		},
		{
			name:     "no code field",
			body:     []byte(`{"error":{"message":"some error"}}`),
			expected: xunfeiErrNone,
		},
		// 10012 system busy → overloaded (503)
		{
			name:     "xunfei 10012 nested error - system busy",
			body:     []byte(`{"error":{"code":10012,"message":"EngineInternalError:The system is busy, please try again later."}}`),
			expected: xunfeiErrOverloaded,
		},
		{
			name:     "xunfei 10012 flat error with sid - system busy",
			body:     []byte(`{"code":10012,"message":"EngineInternalError:The system is busy, please try again later.","sid":"cht000be954@dx19d95a2001eb958700"}`),
			expected: xunfeiErrOverloaded,
		},
		{
			name:     "xunfei 10012 actual format with msg and Sid - system busy",
			body:     []byte(`{"code":10012,"msg":"EngineInternalError:The system is busy, please try again later.","Sid":"cht000ba7f9@dx19d99825f12b992700","timeStamp":"11:35:51.86"}`),
			expected: xunfeiErrOverloaded,
		},
		{
			name:     "xunfei 10012 flat error - system busy",
			body:     []byte(`{"code":10012,"message":"EngineInternalError:The system is busy, please try again later."}`),
			expected: xunfeiErrOverloaded,
		},
		// 10012 bad request → not retryable
		{
			name:     "xunfei 10012 bad request - status code 400",
			body:     []byte(`{"error":{"code":10012,"message":"EngineInternalError:error, status code: 400, status: 400 Bad Request, message: invalid character 'd' looking for beginning of value, body: data:{\"error\":{\"code\":\"ModelArts.81001\",\"message\":\"Inference failed: request param validation error, Value error, message[35] as 'assistant' must have 'content' or 'tool_calls'.Request failed with status: 400 BAD_REQUEST.\"}"}}`),
			expected: xunfeiErrBadRequest,
		},
		{
			name:     "xunfei 10012 bad request - must have content or tool_calls",
			body:     []byte(`{"code":10012,"msg":"EngineInternalError:message[12] as 'assistant' must have 'content' or 'tool_calls'.","Sid":"cht000bf9d0@dx19e248b91e4b958700","timeStamp":"11:33:10.159"}`),
			expected: xunfeiErrBadRequest,
		},
		// 10012 forbidden → not retryable
		{
			name:     "xunfei 10012 forbidden - model not activated",
			body:     []byte(`{"error":{"code":10012,"message":"EngineInternalError:error, status code: 403, status: 403 Forbidden, message: Your account 2000158913 has not activated the model ep-20260416131139-wqh3q. Please activate the model in the KSP Console"}}`),
			expected: xunfeiErrForbidden,
		},
		{
			name:     "xunfei 10012 forbidden - has not activated",
			body:     []byte(`{"code":10012,"msg":"EngineInternalError:Your account has not activated the model ep-20260416131139-wqh3q.","Sid":"cht000b2be4@dx19e2494b75eb8ab700","timeStamp":"11:43:08.887"}`),
			expected: xunfeiErrForbidden,
		},
		// 11210 insufficient credits → retryable
		{
			name:     "xunfei 11210 nested error",
			body:     []byte(`{"error":{"code":11210,"message":"NotEnoughCvError:insufficient credits"}}`),
			expected: xunfeiErrRetryable,
		},
		{
			name:     "xunfei 11210 flat error with Sid",
			body:     []byte(`{"code":11210,"msg":"NotEnoughCvError:insufficient credits","Sid":"cht000bd79e@dx19e207d3c5eb861700","timeStamp":"16:38:58.925"}`),
			expected: xunfeiErrRetryable,
		},
		{
			name:     "xunfei 11210 flat error",
			body:     []byte(`{"code":11210,"message":"NotEnoughCvError:insufficient credits"}`),
			expected: xunfeiErrRetryable,
		},
		// 10010 engine busy → overloaded (503)
		{
			name:     "xunfei 10010 nested error",
			body:     []byte(`{"error":{"code":10010,"message":"RecvFromEngineError:Engine Busy"}}`),
			expected: xunfeiErrOverloaded,
		},
		{
			name:     "xunfei 10010 flat error with Sid",
			body:     []byte(`{"code":10010,"msg":"RecvFromEngineError:Engine Busy","Sid":"cht000b53f6@dx19e2b7df50cb8ab700","timeStamp":"19:57:39.388"}`),
			expected: xunfeiErrOverloaded,
		},
		// 10222 abnormal network error → overloaded (503)
		{
			name:     "xunfei 10222 abnormal network error nested",
			body:     []byte(`{"error":{"code":10222,"message":"AbnormalNetworkError:rpc error: code = Unavailable desc = error reading from server: EOF"}}`),
			expected: xunfeiErrOverloaded,
		},
		{
			name:     "xunfei 10222 abnormal network error flat with Sid",
			body:     []byte(`{"code":10222,"msg":"AbnormalNetworkError:rpc error: code = Unavailable desc = error reading from server: EOF","Sid":"cht000b24b6@dx19e499ffc32b87f700","timeStamp":"16:24:44.695"}`),
			expected: xunfeiErrOverloaded,
		},
		// Non-xunfei errors
		{
			name:     "xunfei other error code nested",
			body:     []byte(`{"error":{"code":10013,"message":"some other error"}}`),
			expected: xunfeiErrNone,
		},
		{
			name:     "xunfei other error code flat",
			body:     []byte(`{"code":10013,"message":"some other error"}`),
			expected: xunfeiErrNone,
		},
		{
			name:     "invalid json",
			body:     []byte(`not json`),
			expected: xunfeiErrNone,
		},
		{
			name:     "openai style error",
			body:     []byte(`{"error":{"message":"invalid request","type":"invalid_request_error"}}`),
			expected: xunfeiErrNone,
		},
		{
			name:     "code zero nested",
			body:     []byte(`{"error":{"code":0,"message":"ok"}}`),
			expected: xunfeiErrNone,
		},
		{
			name:     "code zero flat",
			body:     []byte(`{"code":0,"message":"ok"}`),
			expected: xunfeiErrNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyXunfeiError(tt.body)
			if result != tt.expected {
				t.Errorf("classifyXunfeiError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestXunfeiStatusErr(t *testing.T) {
	tests := []struct {
		name         string
		cls          xunfeiErrorClass
		expectedCode int
	}{
		{
			name:         "bad request returns 400",
			cls:          xunfeiErrBadRequest,
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "forbidden returns 403",
			cls:          xunfeiErrForbidden,
			expectedCode: http.StatusForbidden,
		},
		{
			name:         "overloaded returns 503",
			cls:          xunfeiErrOverloaded,
			expectedCode: http.StatusServiceUnavailable,
		},
		{
			name:         "retryable returns 429",
			cls:          xunfeiErrRetryable,
			expectedCode: http.StatusTooManyRequests,
		},
		{
			name:         "none returns 500",
			cls:          xunfeiErrNone,
			expectedCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := xunfeiStatusErr(tt.cls, "test body")
			if err.StatusCode() != tt.expectedCode {
				t.Errorf("xunfeiStatusErr(%v).StatusCode() = %d, want %d", tt.cls, err.StatusCode(), tt.expectedCode)
			}
		})
	}
}

func TestXunfeiStatusErrWithRetryAfter(t *testing.T) {
	retryAfter := 10 * time.Second
	err := xunfeiStatusErrWithRetryAfter(xunfeiErrRetryable, retryAfter)
	if err.StatusCode() != http.StatusTooManyRequests {
		t.Errorf("StatusCode() = %d, want %d", err.StatusCode(), http.StatusTooManyRequests)
	}
	if err.RetryAfter() == nil {
		t.Error("RetryAfter() should not be nil")
	} else if *err.RetryAfter() != retryAfter {
		t.Errorf("RetryAfter() = %v, want %v", *err.RetryAfter(), retryAfter)
	}
}

func TestIsXunfeiRetryableError(t *testing.T) {
	if isXunfeiRetryableError([]byte(`{"error":{"code":10012,"message":"The system is busy"}}`)) {
		t.Error("system busy should be overloaded, not retryable")
	}
	if !isXunfeiRetryableOrOverloaded([]byte(`{"error":{"code":10012,"message":"The system is busy"}}`)) {
		t.Error("system busy should be retryable-or-overloaded")
	}
	if !isXunfeiRetryableError([]byte(`{"error":{"code":11210,"message":"NotEnoughCvError"}}`)) {
		t.Error("11210 should be retryable")
	}
	if isXunfeiRetryableError([]byte(`{"error":{"code":10012,"message":"status code: 400, must have 'content' or 'tool_calls'"}}`)) {
		t.Error("bad request should not be retryable")
	}
	if isXunfeiRetryableError([]byte(`{"error":{"code":10012,"message":"status code: 403, has not activated"}}`)) {
		t.Error("forbidden should not be retryable")
	}
}

func TestXunfeiRetryConfigDefaults(t *testing.T) {
	cfg := config.XunfeiRetryConfig{}

	if cfg.EffectiveMaxRetries() != 3 {
		t.Errorf("EffectiveMaxRetries() = %d, want 3", cfg.EffectiveMaxRetries())
	}
	if cfg.EffectiveInitialWait() != 10000 {
		t.Errorf("EffectiveInitialWait() = %d, want 10000", cfg.EffectiveInitialWait())
	}
	if cfg.EffectiveMaxWait() != 16000 {
		t.Errorf("EffectiveMaxWait() = %d, want 16000", cfg.EffectiveMaxWait())
	}
	if cfg.EffectiveMultiplier() != 2.0 {
		t.Errorf("EffectiveMultiplier() = %v, want 2.0", cfg.EffectiveMultiplier())
	}

	waits := cfg.WaitDurations()
	if len(waits) != 3 {
		t.Errorf("len(WaitDurations()) = %d, want 3", len(waits))
	}
	if waits[0] != 10000 {
		t.Errorf("WaitDurations()[0] = %d, want 10000", waits[0])
	}
}

func TestXunfeiRetryConfigCustom(t *testing.T) {
	cfg := config.XunfeiRetryConfig{
		MaxRetries:  8,
		InitialWait: 1000,
		MaxWait:     30000,
		Multiplier:  1.5,
	}

	if cfg.EffectiveMaxRetries() != 8 {
		t.Errorf("EffectiveMaxRetries() = %d, want 8", cfg.EffectiveMaxRetries())
	}
	if cfg.EffectiveInitialWait() != 1000 {
		t.Errorf("EffectiveInitialWait() = %d, want 1000", cfg.EffectiveInitialWait())
	}
	if cfg.EffectiveMaxWait() != 30000 {
		t.Errorf("EffectiveMaxWait() = %d, want 30000", cfg.EffectiveMaxWait())
	}
	if cfg.EffectiveMultiplier() != 1.5 {
		t.Errorf("EffectiveMultiplier() = %v, want 1.5", cfg.EffectiveMultiplier())
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
