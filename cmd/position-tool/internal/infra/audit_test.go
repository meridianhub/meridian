package infra

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewImportAuditLogger(t *testing.T) {
	t.Run("creates logger with generated import ID", func(t *testing.T) {
		logger := NewImportAuditLogger(ImportAuditLoggerConfig{
			SchemaName: "org_acme_bank",
		})
		require.NotNil(t, logger)
		assert.NotEmpty(t, logger.ImportID())
		assert.Equal(t, int64(0), logger.EventsPublished())
	})

	t.Run("uses provided import ID", func(t *testing.T) {
		logger := NewImportAuditLogger(ImportAuditLoggerConfig{
			SchemaName: "org_acme_bank",
			ImportID:   "custom-import-id",
		})
		require.NotNil(t, logger)
		assert.Equal(t, "custom-import-id", logger.ImportID())
	})

	t.Run("uses import ID as correlation ID if not provided", func(t *testing.T) {
		logger := NewImportAuditLogger(ImportAuditLoggerConfig{
			SchemaName: "org_acme_bank",
			ImportID:   "import-123",
		})
		require.NotNil(t, logger)
		// Internal field check - correlation ID equals import ID
		assert.Equal(t, "import-123", logger.correlationID)
	})

	t.Run("uses provided correlation ID", func(t *testing.T) {
		logger := NewImportAuditLogger(ImportAuditLoggerConfig{
			SchemaName:    "org_acme_bank",
			ImportID:      "import-123",
			CorrelationID: "corr-456",
		})
		require.NotNil(t, logger)
		assert.Equal(t, "corr-456", logger.correlationID)
	})
}

func TestImportAuditLogger_NilPublisher(t *testing.T) {
	ctx := context.Background()

	logger := NewImportAuditLogger(ImportAuditLoggerConfig{
		Publisher:  nil, // No publisher
		SchemaName: "org_acme_bank",
	})

	t.Run("LogBatchImport is no-op with nil publisher", func(t *testing.T) {
		err := logger.LogBatchImport(ctx, 1, 100, "acc-123", "USD", "test-user")
		assert.NoError(t, err)
		assert.Equal(t, int64(0), logger.EventsPublished())
	})

	t.Run("LogImportComplete is no-op with nil publisher", func(t *testing.T) {
		err := logger.LogImportComplete(ctx, 1000, 10, 5*time.Minute, "test-user")
		assert.NoError(t, err)
		assert.Equal(t, int64(0), logger.EventsPublished())
	})
}

func TestNoOpAuditLogger(t *testing.T) {
	logger := NoOpAuditLogger()

	require.NotNil(t, logger)
	assert.NotEmpty(t, logger.ImportID())
	assert.Nil(t, logger.publisher)

	// All operations should be no-ops
	ctx := context.Background()

	err := logger.LogBatchImport(ctx, 1, 100, "acc", "USD", "user")
	assert.NoError(t, err)

	err = logger.LogImportComplete(ctx, 100, 1, time.Second, "user")
	assert.NoError(t, err)

	assert.Equal(t, int64(0), logger.EventsPublished())
}

func TestImportAuditLoggerConfig(t *testing.T) {
	config := ImportAuditLoggerConfig{
		Publisher:     nil,
		SchemaName:    "org_test_tenant",
		ImportID:      "import-abc",
		CorrelationID: "corr-xyz",
	}

	assert.Equal(t, "org_test_tenant", config.SchemaName)
	assert.Equal(t, "import-abc", config.ImportID)
	assert.Equal(t, "corr-xyz", config.CorrelationID)
}

func TestOperationInitialImport(t *testing.T) {
	assert.Equal(t, "INITIAL_IMPORT", OperationInitialImport)
}

// Note: Integration tests that require a real Kafka publisher
// are in audit_integration_test.go

func TestImportAuditLogger_ImportID(t *testing.T) {
	logger1 := NewImportAuditLogger(ImportAuditLoggerConfig{})
	logger2 := NewImportAuditLogger(ImportAuditLoggerConfig{})

	// Auto-generated IDs should be unique
	assert.NotEqual(t, logger1.ImportID(), logger2.ImportID())
}

func TestImportAuditLogger_EventsPublished(t *testing.T) {
	// Without a real publisher, EventsPublished stays at 0
	logger := NewImportAuditLogger(ImportAuditLoggerConfig{})
	assert.Equal(t, int64(0), logger.EventsPublished())

	// Even after "logging" events, count stays 0 with nil publisher
	ctx := context.Background()
	_ = logger.LogBatchImport(ctx, 1, 100, "acc", "USD", "user")
	assert.Equal(t, int64(0), logger.EventsPublished())
}

func BenchmarkImportAuditLogger_LogBatchImport_NilPublisher(b *testing.B) {
	ctx := context.Background()
	logger := NewImportAuditLogger(ImportAuditLoggerConfig{
		SchemaName: "org_acme_bank",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = logger.LogBatchImport(ctx, 1, 100, "acc-123", "USD", "test-user")
	}
}
