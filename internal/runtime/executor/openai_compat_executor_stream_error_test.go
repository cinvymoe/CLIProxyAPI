package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
)

func TestWrapTransientNetworkError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"unexpected EOF", io.ErrUnexpectedEOF},
		{"wrapped unexpected EOF", fmt.Errorf("read body: %w", io.ErrUnexpectedEOF)},
		{"connection reset", &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapTransientNetworkError(tt.err)
			se, ok := got.(statusErr)
			if !ok {
				t.Fatalf("wrapTransientNetworkError(%v) = %T, want statusErr", tt.err, got)
			}
			if se.code != http.StatusBadGateway {
				t.Fatalf("statusErr code = %d, want %d", se.code, http.StatusBadGateway)
			}
			if se.Error() != tt.err.Error() {
				t.Fatalf("statusErr msg = %q, want %q", se.Error(), tt.err.Error())
			}
		})
	}
}

func TestWrapTransientNetworkErrorPassthrough(t *testing.T) {
	for _, err := range []error{
		bufio.ErrTooLong,
		context.Canceled,
		context.DeadlineExceeded,
		fmt.Errorf("some other error"),
	} {
		if got := wrapTransientNetworkError(err); got != err {
			t.Fatalf("wrapTransientNetworkError(%v) = %v, want passthrough", err, got)
		}
	}
	if got := wrapTransientNetworkError(nil); got != nil {
		t.Fatalf("wrapTransientNetworkError(nil) = %v, want nil", got)
	}
}
