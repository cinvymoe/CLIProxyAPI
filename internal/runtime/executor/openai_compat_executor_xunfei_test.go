package executor

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
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

func TestXunfeiRetryConfigDefaults(t *testing.T) {
	cfg := config.XunfeiRetryConfig{}

	if cfg.EffectiveMaxRetries() != 3 {
		t.Errorf("EffectiveMaxRetries() = %d, want 3", cfg.EffectiveMaxRetries())
	}
	if cfg.EffectiveInitialWait() != 2000 {
		t.Errorf("EffectiveInitialWait() = %d, want 2000", cfg.EffectiveInitialWait())
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
	if waits[0] != 2000 {
		t.Errorf("WaitDurations()[0] = %d, want 2000", waits[0])
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
