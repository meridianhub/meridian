package db

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Test error sentinels for simulating database failures.
var (
	errDatabaseConnectionLost = errors.New("database connection lost")
	errSchemaDoesNotExist     = errors.New("schema does not exist")
)

func TestWithGormTenantScope_SetsSearchPath(t *testing.T) {
	// Create mock database
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Setup context with tenant
	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// WithGormTenantScope requires an active transaction
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("org_acme_bank").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	tx := gormDB.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)

	// Execute
	result, err := WithGormTenantScope(ctx, tx)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, result)
	_ = tx.Rollback()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormTenantScope_MissingContext_ReturnsError(t *testing.T) {
	// Create mock database
	mockDB, _, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Context without organization
	ctx := context.Background()

	// Execute
	result, err := WithGormTenantScope(ctx, gormDB)

	// Assert
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)
}

func TestMustWithGormTenantScope_MissingContext_Panics(t *testing.T) {
	// Create mock database
	mockDB, _, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Context without organization
	ctx := context.Background()

	// Assert panic
	assert.Panics(t, func() {
		MustWithGormTenantScope(ctx, gormDB)
	})
}

func TestWithGormTenantTransaction_SetsSearchPathAndExecutes(t *testing.T) {
	// Create mock database
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Setup context with tenant
	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Expect transaction begin, SET LOCAL, schema check, and commit
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("org_acme_bank").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectCommit()

	// Execute
	executed := false
	err = WithGormTenantTransaction(ctx, gormDB, func(_ *gorm.DB) error {
		executed = true
		return nil
	})

	// Assert
	require.NoError(t, err)
	assert.True(t, executed, "transaction function should have been executed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormTenantTransaction_MissingContext_ReturnsError(t *testing.T) {
	// Create mock database
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Context without organization
	ctx := context.Background()

	// Expect transaction begin and rollback (due to error)
	mock.ExpectBegin()
	mock.ExpectRollback()

	// Execute
	executed := false
	err = WithGormTenantTransaction(ctx, gormDB, func(_ *gorm.DB) error {
		executed = true
		return nil
	})

	// Assert
	require.Error(t, err)
	assert.False(t, executed, "transaction function should not have been executed")
	assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormTenantScope_SpecialCharacters_QuotedProperly(t *testing.T) {
	testCases := []struct {
		name           string
		tenantID       string
		expectedSchema string
	}{
		{
			name:           "simple org id",
			tenantID:       "acme",
			expectedSchema: `"org_acme"`,
		},
		{
			name:           "org id with underscore",
			tenantID:       "acme_bank",
			expectedSchema: `"org_acme_bank"`,
		},
		{
			name:           "org id with numbers",
			tenantID:       "bank123",
			expectedSchema: `"org_bank123"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock database
			mockDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer mockDB.Close()

			// Create GORM instance with mock
			gormDB, err := gorm.Open(postgres.New(postgres.Config{
				Conn: mockDB,
			}), &gorm.Config{})
			require.NoError(t, err)

			// Setup context
			tenantID := tenant.TenantID(tc.tenantID)
			ctx := tenant.WithTenant(context.Background(), tenantID)

			// WithGormTenantScope requires an active transaction
			mock.ExpectBegin()
			expected := "SET LOCAL search_path TO " + tc.expectedSchema + ", public"
			mock.ExpectExec(expected).
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT EXISTS`).
				WithArgs("org_" + tc.tenantID).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			mock.ExpectRollback()

			tx := gormDB.WithContext(ctx).Begin()
			require.NoError(t, tx.Error)

			// Execute
			result, err := WithGormTenantScope(ctx, tx)

			// Assert
			require.NoError(t, err)
			assert.NotNil(t, result)
			_ = tx.Rollback()
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestWithGormTenantScope_DatabaseError_ReturnsError(t *testing.T) {
	// This tests the error path when SET LOCAL search_path fails (e.g., database connection issue)
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Setup context with tenant
	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// WithGormTenantScope requires an active transaction
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnError(errDatabaseConnectionLost)
	mock.ExpectRollback()

	tx := gormDB.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)

	// Execute
	result, err := WithGormTenantScope(ctx, tx)

	// Assert - should return error when SET LOCAL fails
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to set tenant schema scope")
	assert.ErrorIs(t, err, errDatabaseConnectionLost)
	_ = tx.Rollback()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormTenantTransaction_DatabaseError_ReturnsError(t *testing.T) {
	// This tests the error propagation through WithGormTenantTransaction
	// when SET LOCAL search_path fails
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Setup context with tenant
	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	// Expect transaction begin, SET LOCAL failure, and rollback
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnError(errSchemaDoesNotExist)
	mock.ExpectRollback()

	// Execute
	executed := false
	err = WithGormTenantTransaction(ctx, gormDB, func(_ *gorm.DB) error {
		executed = true
		return nil
	})

	// Assert - function should not have been executed, error should propagate
	require.Error(t, err)
	assert.False(t, executed, "transaction function should not have been executed")
	assert.Contains(t, err.Error(), "failed to set tenant schema scope")
	assert.ErrorIs(t, err, errSchemaDoesNotExist)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormTenantScope_NonExistentSchema_ReturnsError(t *testing.T) {
	// This tests that WithGormTenantScope returns ErrTenantSchemaNotProvisioned
	// when the schema does not exist in pg_namespace.
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	tenantID := tenant.TenantID("nonexistent_tenant")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_nonexistent_tenant", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("org_nonexistent_tenant").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	tx := gormDB.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)

	result, err := WithGormTenantScope(ctx, tx)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrTenantSchemaNotProvisioned)
	assert.Contains(t, err.Error(), "org_nonexistent_tenant")
	_ = tx.Rollback()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormTenantScope_SchemaVerificationFailure_ReturnsError(t *testing.T) {
	// This tests the error path when the schema existence query itself fails.
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	tenantID := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tenantID)

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs("org_acme_bank").
		WillReturnError(errDatabaseConnectionLost)
	mock.ExpectRollback()

	tx := gormDB.WithContext(ctx).Begin()
	require.NoError(t, tx.Error)

	result, err := WithGormTenantScope(ctx, tx)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to verify tenant schema existence")
	assert.ErrorIs(t, err, errDatabaseConnectionLost)
	_ = tx.Rollback()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormTenantScope_MaliciousSchemaNames_ProperlyEscaped(t *testing.T) {
	// These test cases verify that pq.QuoteIdentifier properly escapes
	// potentially malicious tenant IDs to prevent SQL injection.
	// Note: pq.QuoteIdentifier truncates at null bytes and uses lowercase.
	testCases := []struct {
		name           string
		tenantID       string
		expectedSchema string // pq.QuoteIdentifier output
	}{
		{
			name:     "SQL injection attempt with quote",
			tenantID: `evil"; drop table users; --`,
			// pq.QuoteIdentifier escapes double quotes by doubling them
			expectedSchema: `"org_evil""; drop table users; --"`,
		},
		{
			name:           "SQL injection attempt with semicolon",
			tenantID:       "evil; drop table users",
			expectedSchema: `"org_evil; drop table users"`,
		},
		{
			name:     "null byte injection",
			tenantID: "evil\x00attack",
			// pq.QuoteIdentifier truncates at the null byte
			expectedSchema: `"org_evil"`,
		},
		{
			name:           "unicode escape sequence for double quote",
			tenantID:       "evil\u0022injection",
			expectedSchema: `"org_evil""injection"`, // \u0022 is a double quote, escaped by doubling
		},
		{
			name:           "schema traversal attempt with dot",
			tenantID:       "public.accounts",
			expectedSchema: `"org_public.accounts"`,
		},
		{
			name:           "backslash injection",
			tenantID:       `evil\ninjection`,
			expectedSchema: `"org_evil\ninjection"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock database with QueryMatcherEqual to avoid regex interpretation
			mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer mockDB.Close()

			// Create GORM instance with mock
			gormDB, err := gorm.Open(postgres.New(postgres.Config{
				Conn: mockDB,
			}), &gorm.Config{})
			require.NoError(t, err)

			// Setup context with potentially malicious org ID
			tenantID := tenant.TenantID(tc.tenantID)
			ctx := tenant.WithTenant(context.Background(), tenantID)

			// WithGormTenantScope requires an active transaction
			mock.ExpectBegin()
			expected := "SET LOCAL search_path TO " + tc.expectedSchema + ", public"
			mock.ExpectExec(expected).
				WillReturnResult(sqlmock.NewResult(0, 0))
			schemaName := tenant.TenantID(tc.tenantID).SchemaName()
			mock.ExpectQuery("SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname = $1)").
				WithArgs(schemaName).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			mock.ExpectRollback()

			tx := gormDB.WithContext(ctx).Begin()
			require.NoError(t, tx.Error)

			// Execute
			result, err := WithGormTenantScope(ctx, tx)

			// Assert - even with malicious input, the function should work
			// because pq.QuoteIdentifier properly escapes the schema name
			require.NoError(t, err)
			assert.NotNil(t, result)
			_ = tx.Rollback()
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
