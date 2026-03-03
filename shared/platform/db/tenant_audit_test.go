package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var errAuditTestDBFailed = errors.New("database connection lost")

// logEntry represents a parsed JSON log line for assertion
type logEntry struct {
	Level      string `json:"level"`
	Msg        string `json:"msg"`
	TenantHash string `json:"tenant_hash"`
	Schema     string `json:"schema"`
	Service    string `json:"service"`
}

func newAuditMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockConn, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { mockConn.Close() })

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockConn,
	}), &gorm.Config{})
	require.NoError(t, err)

	return gormDB, mock
}

// findLogEntry finds a log entry with the given message in a multi-line JSON log buffer.
func findLogEntry(t *testing.T, buf *bytes.Buffer, msg string) *logEntry {
	t.Helper()
	for _, line := range strings.Split(buf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry logEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Msg == msg {
			return &entry
		}
	}
	return nil
}

func TestTenantAudit_LogsSchemaAccess(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	gormDB, mock := newAuditMockGormDB(t)

	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := db.WithGormTenantScopeAndLogger(ctx, gormDB, logger)
	require.NoError(t, err)

	entry := findLogEntry(t, &buf, "tenant.schema.access")
	require.NotNil(t, entry, "expected tenant.schema.access audit log, got: %s", buf.String())

	assert.Equal(t, "INFO", entry.Level)
	assert.NotEmpty(t, entry.TenantHash, "tenant_hash should be set")
	assert.Equal(t, "org_acme_bank", entry.Schema)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTenantAudit_LogsSchemaAccessWithService(t *testing.T) {
	var buf bytes.Buffer
	// Create logger with service attribute pre-set (as services would do at startup)
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger = logger.With("service", "current-account")

	gormDB, mock := newAuditMockGormDB(t)

	tenantID := tenant.TenantID("beta_corp")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	mock.ExpectExec(`SET LOCAL search_path TO "org_beta_corp", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := db.WithGormTenantScopeAndLogger(ctx, gormDB, logger)
	require.NoError(t, err)

	entry := findLogEntry(t, &buf, "tenant.schema.access")
	require.NotNil(t, entry, "expected tenant.schema.access audit log, got: %s", buf.String())

	assert.NotEmpty(t, entry.TenantHash, "tenant_hash should be set")
	assert.Equal(t, "org_beta_corp", entry.Schema)
	assert.Equal(t, "current-account", entry.Service)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTenantAudit_NoLogOnMissingContext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	gormDB, _ := newAuditMockGormDB(t)
	ctx := context.Background() // no tenant

	_, err := db.WithGormTenantScopeAndLogger(ctx, gormDB, logger)
	require.Error(t, err)
	assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)

	// No audit log should be emitted on failure
	assert.Empty(t, buf.String(), "should not log audit entry when tenant context is missing")
}

func TestTenantAudit_NoLogOnDatabaseError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	gormDB, mock := newAuditMockGormDB(t)

	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnError(errAuditTestDBFailed)

	_, err := db.WithGormTenantScopeAndLogger(ctx, gormDB, logger)
	require.Error(t, err)

	// Error/warn logs are expected, but the audit log ("tenant.schema.access") should NOT be emitted
	assert.NotContains(t, buf.String(), "tenant.schema.access",
		"should not log audit entry when SET LOCAL fails")
	assert.NoError(t, mock.ExpectationsWereMet())
}
