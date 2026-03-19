package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogger_DebugContext(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, observability.LogLevelDebug)

	ctx := context.Background()
	logger.DebugContext(ctx, "debug message")

	var entry observability.LogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, observability.LogLevelDebug, entry.Level)
	assert.Equal(t, "debug message", entry.Message)
}

func TestLogger_WarnContext(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, observability.LogLevelDebug)

	ctx := context.Background()
	logger.WarnContext(ctx, "warn message")

	var entry observability.LogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, observability.LogLevelWarn, entry.Level)
}

func TestLogger_ErrorContext(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, observability.LogLevelDebug)

	ctx := context.Background()
	logger.ErrorContext(ctx, "error message")

	var entry observability.LogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, observability.LogLevelError, entry.Level)
}

func TestLogger_ContextWithTenant(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, observability.LogLevelDebug)

	ctx := tenant.WithTenant(context.Background(), "acme_bank")
	logger.InfoContext(ctx, "tenant message")

	var entry observability.LogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "acme_bank", entry.TenantID)
}

func TestLogger_ContextWithCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, observability.LogLevelDebug)

	ctx := observability.WithCorrelationID(context.Background(), "corr-123")
	logger.InfoContext(ctx, "correlated message")

	var entry observability.LogEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "corr-123", entry.CorrelationID)
}

func TestLogger_ShouldLog_UnknownLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, "unknown-level")

	// Unknown configured level should default to info behavior.
	logger.Debug("should not appear") // debug < info
	assert.Empty(t, buf.String())

	logger.Info("should appear")
	assert.NotEmpty(t, buf.String())
}

func TestLogger_ShouldLog_FiltersBelowLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, observability.LogLevelWarn)

	logger.Debug("should not appear")
	assert.Empty(t, buf.String())

	logger.Info("should not appear")
	assert.Empty(t, buf.String())

	logger.Warn("should appear")
	assert.NotEmpty(t, buf.String())
}
