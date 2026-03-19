package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},          // empty defaults to info
		{"unknown", slog.LevelInfo},   // unknown defaults to info
		{"TRACE", slog.LevelInfo},     // unsupported defaults to info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseLogLevel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGobreakerStateToMetricState(t *testing.T) {
	tests := []struct {
		name     string
		input    gobreaker.State
		expected caobservability.CircuitBreakerState
	}{
		{"closed", gobreaker.StateClosed, caobservability.CircuitBreakerStateClosed},
		{"half-open", gobreaker.StateHalfOpen, caobservability.CircuitBreakerStateHalfOpen},
		{"open", gobreaker.StateOpen, caobservability.CircuitBreakerStateOpen},
		{"unknown defaults to closed", gobreaker.State(99), caobservability.CircuitBreakerStateClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gobreakerStateToMetricState(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMakeCircuitBreakerCallback(t *testing.T) {
	callback := makeCircuitBreakerCallback()
	assert.NotNil(t, callback)

	// Verify callback does not panic when invoked
	assert.NotPanics(t, func() {
		callback("test-service", gobreaker.StateClosed, gobreaker.StateOpen)
	})

	assert.NotPanics(t, func() {
		callback("test-service", gobreaker.StateOpen, gobreaker.StateHalfOpen)
	})

	assert.NotPanics(t, func() {
		callback("test-service", gobreaker.StateHalfOpen, gobreaker.StateClosed)
	})
}

func TestNewPartyClientWrapper(t *testing.T) {
	client, cleanup, err := partyclient.New(partyclient.Config{
		Target:  "localhost:50055",
		Timeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	defer cleanup()

	wrapper := NewPartyClientWrapper(client)
	assert.NotNil(t, wrapper)
}

func TestPartyClientWrapper_ValidateParty_NoServer(t *testing.T) {
	client, cleanup, err := partyclient.New(partyclient.Config{
		Target:  "localhost:50055",
		Timeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	defer cleanup()

	wrapper := NewPartyClientWrapper(client)
	// Should fail because no real gRPC server is running, but code path is covered
	err = wrapper.ValidateParty(context.Background(), "PARTY-001")
	assert.Error(t, err)
}

func TestPartyClientWrapper_GetParty_NoServer(t *testing.T) {
	client, cleanup, err := partyclient.New(partyclient.Config{
		Target:  "localhost:50055",
		Timeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	defer cleanup()

	wrapper := NewPartyClientWrapper(client)
	_, err = wrapper.GetParty(context.Background(), "PARTY-001")
	assert.Error(t, err)
}

func TestPartyClientWrapper_Close(t *testing.T) {
	client, cleanup, err := partyclient.New(partyclient.Config{
		Target:  "localhost:50055",
		Timeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	defer cleanup()

	wrapper := NewPartyClientWrapper(client)
	err = wrapper.Close()
	assert.NoError(t, err)
}
