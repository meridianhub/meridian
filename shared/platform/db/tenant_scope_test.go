package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// errMockExecFailed is a sentinel error for testing exec failures
var errMockExecFailed = errors.New("mock exec failed")

// mockDB implements db.DB for unit testing
type mockDB struct {
	execCalled bool
	execQuery  string
	execErr    error
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
	return nil
}

func (m *mockDB) BeginTx(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
	return nil, nil
}

func (m *mockDB) Ping(_ context.Context) error {
	return nil
}

func (m *mockDB) Close() error {
	return nil
}

func TestWithTenantScope_Success(t *testing.T) {
	mock := &mockDB{}
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

func TestWithTenantScope_MissingOrganizationContext(t *testing.T) {
	mock := &mockDB{}
	ctx := context.Background() // No organization in context

	result, err := db.WithTenantScope(ctx, mock)

	if err == nil {
		t.Fatal("WithTenantScope should return error for missing tenant context")
	}
	if !errors.Is(err, tenant.ErrMissingTenantContext) {
		t.Errorf("error = %v, want ErrMissingOrganizationContext", err)
	}
	if result != nil {
		t.Error("WithTenantScope should return nil DB on error")
	}
	if mock.execCalled {
		t.Error("WithTenantScope should not execute query when organization missing")
	}
}

func TestWithTenantScope_ExecError(t *testing.T) {
	mock := &mockDB{execErr: errMockExecFailed}
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
			mock := &mockDB{}
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
	mock := &mockDB{}
	orgID := tenant.MustNewTenantID("test_org")
	ctx := tenant.WithTenant(context.Background(), orgID)

	// Should not panic
	result := db.MustWithTenantScope(ctx, mock)

	if result != mock {
		t.Error("MustWithTenantScope should return the same DB instance")
	}
}

func TestMustWithTenantScope_Panics(t *testing.T) {
	mock := &mockDB{}
	ctx := context.Background() // No organization

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustWithTenantScope should panic when organization missing")
		}
	}()

	db.MustWithTenantScope(ctx, mock)
}
