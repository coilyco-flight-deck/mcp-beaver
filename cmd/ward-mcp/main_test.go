package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp/internal/mcpserver"
)

func TestConnectProxyWithRetryRecovers(t *testing.T) {
	attempts := 0
	srv, err := connectProxyWithRetry(context.Background(), time.Second, time.Millisecond, func(context.Context) (*mcpserver.Server, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("upstream is warming")
		}
		return &mcpserver.Server{}, nil
	})
	if err != nil {
		t.Fatalf("connectProxyWithRetry: %v", err)
	}
	if srv == nil {
		t.Fatal("connectProxyWithRetry returned a nil server")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestConnectProxyWithRetryTimesOut(t *testing.T) {
	_, err := connectProxyWithRetry(context.Background(), 20*time.Millisecond, time.Millisecond, func(context.Context) (*mcpserver.Server, error) {
		return nil, errors.New("upstream unavailable")
	})
	if err == nil {
		t.Fatal("connectProxyWithRetry succeeded, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out after") || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("connectProxyWithRetry error = %q", err)
	}
}
