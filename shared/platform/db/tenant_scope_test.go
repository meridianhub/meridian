package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// errMockExecFailed is a sentinel error for testing exec failures
var errMockExecFailed = errors.New("mock exec failed")

// mockDB implements db.DB for unit testing.
// It uses sqlmock internally for QueryRowContext to produce valid *sql.Row values.
type mockDB struct {
	execCalled   bool
	execQuery    string
	execErr      error
	schemaExists bool // controls what the schema existence check returns
	queryRowErr  error
	sqlDB        *sql.DB
	sqlMock      sqlmock.Sqlmock
}

func newMockDB(t *testing.T, schemaExists bool) *mockDB {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	return &mockDB{
		schemaExists: schemaExists,
		sqlDB:        sqlDB,
		sqlMock:      mock,
	}
}

func (m *mockDB) QueryContext(_ context.Context, _ string, _ ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (m *mockDB) ExecContext(_ context.Context, query string, _ ...interface{}) (sql.Result, error) {
	m.execCalled = true
	m.execQuery = query
	return nil, m.execErr
}

func (m *mockDB) QueryRowContext(_ context.Context, _ string, _ ...interface{}) *sql.Row {
	if m.queryRowErr != nil {
		m.sqlMock.ExpectQuery("SELECT").WillReturnError(m.queryRowErr)
	} else {
		m.sqlMock.ExpectQuery("SELECT").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(m.schemaExists))
	}
	return m.sqlDB.QueryRow("SELECT 1")
}

func (m *mockDB) BeginTx(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, nil
}

func (m *mockDB) Ping(_ context.Context) error {
	return nil
}

func (m *mockDB) Close() error {
	return m.sqlDB.Close()
}

func TestWithTenantScope_Success(t *testing.T) {
	mock := newMockDB(t, true)
	defer mock.Close()
	orgID := tenant.MustNewTenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), orgID)

	result, err := db.WithTenantScope(ctx, mock)
	if err != nil {
		t.Fatalf("WithTenantScope returned unexpected error: %v", err)
	}
	if result != mock {
		t.Error("WithTenantScope should return the same DB instance")
	}
	if !mock.execCalled {
		t.Error("WithTenantScope should execute SET LOCAL query")
	}
	// Schema name should be quoted and include public
	expected := `SET LOCAL search_path TO "org_acme_bank", public`
	if mock.execQuery != expected {
		t.Errorf("Query = %q, want %q", mock.execQuery, expected)
	}
}

func TestWithTenantScope_MissingTenantContext(t *testing.T) {
	mock := newMockDB(t, true)
	defer mock.Close()
	ctx := context.Background() // No tenant in context

	result, err := db.WithTenantScope(ctx, mock)

	if err == nil {
		t.Fatal("WithTenantScope should return error for missing tenant context")
	}
	if !errors.Is(err, tenant.ErrMissingTenantContext) {
		t.Errorf("error = %v, want ErrMissingTenantContext", err)
	}
	if result != nil {
		t.Error("WithTenantScope should return nil DB on error")
	}
	if mock.execCalled {
		t.Error("WithTenantScope should not execute query when tenant missing")
	}
}

func TestWithTenantScope_ExecError(t *testing.T) {
	mock := newMockDB(t, true)
	defer mock.Close()
	mock.execErr = errMockExecFailed
	orgID := tenant.MustNewTenantID("test_org")
	ctx := tenant.WithTenant(context.Background(), orgID)

	result, err := db.WithTenantScope(ctx, mock)

	if err == nil {
		t.Fatal("WithTenantScope should return error when exec fails")
	}
	if !errors.Is(err, errMockExecFailed) {
		t.Errorf("error should wrap exec error, got: %v", err)
	}
	if result != nil {
		t.Error("WithTenantScope should return nil DB on error")
	}
}

func TestWithTenantScope_NonExistentSchema_ReturnsError(t *testing.T) {
	mock := newMockDB(t, false) // schema does not exist
	defer mock.Close()
	orgID := tenant.MustNewTenantID("nonexistent")
	ctx := tenant.WithTenant(context.Background(), orgID)

	result, err := db.WithTenantScope(ctx, mock)

	if err == nil {
		t.Fatal("WithTenantScope should return error for non-existent schema")
	}
	if !errors.Is(err, db.ErrTenantSchemaNotProvisioned) {
		t.Errorf("error = %v, want ErrTenantSchemaNotProvisioned", err)
	}
	if result != nil {
		t.Error("WithTenantScope should return nil DB on error")
	}
}

func TestWithTenantScope_SchemaNameQuoting(t *testing.T) {
	tests := []struct {
		name           string
		orgID          string
		expectedSchema string
	}{
		{
			name:           "lowercase",
			orgID:          "acme",
			expectedSchema: `"org_acme"`,
		},
		{
			name:           "uppercase_normalized",
			orgID:          "ACME",
			expectedSchema: `"org_acme"`, // SchemaName() lowercases
		},
		{
			name:           "with_underscore",
			orgID:          "acme_bank",
			expectedSchema: `"org_acme_bank"`,
		},
		{
			name:           "with_numbers",
			orgID:          "bank123",
			expectedSchema: `"org_bank123"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockDB(t, true)
			defer mock.Close()
			orgID := tenant.MustNewTenantID(tt.orgID)
			ctx := tenant.WithTenant(context.Background(), orgID)

			_, err := db.WithTenantScope(ctx, mock)
			if err != nil {
				t.Fatalf("WithTenantScope returned unexpected error: %v", err)
			}
			expected := "SET LOCAL search_path TO " + tt.expectedSchema + ", public"
			if mock.execQuery != expected {
				t.Errorf("Query = %q, want %q", mock.execQuery, expected)
			}
		})
	}
}

func TestMustWithTenantScope_Success(t *testing.T) {
	mock := newMockDB(t, true)
	defer mock.Close()
	orgID := tenant.MustNewTenantID("test_org")
	ctx := tenant.WithTenant(context.Background(), orgID)

	// Should not panic
	result := db.MustWithTenantScope(ctx, mock)

	if result != mock {
		t.Error("MustWithTenantScope should return the same DB instance")
	}
}

func TestMustWithTenantScope_Panics(t *testing.T) {
	mock := newMockDB(t, true)
	defer mock.Close()
	ctx := context.Background() // No tenant

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustWithTenantScope should panic when tenant missing")
		}
	}()

	db.MustWithTenantScope(ctx, mock)
}
