package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/organization"
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

func TestWithOrganizationScope_Success(t *testing.T) {
	mock := &mockDB{}
	orgID := organization.MustNewOrganizationID("acme_bank")
	ctx := organization.WithOrganization(context.Background(), orgID)

	result, err := db.WithOrganizationScope(ctx, mock)
	if err != nil {
		t.Fatalf("WithOrganizationScope returned unexpected error: %v", err)
	}
	if result != mock {
		t.Error("WithOrganizationScope should return the same DB instance")
	}
	if !mock.execCalled {
		t.Error("WithOrganizationScope should execute SET LOCAL query")
	}
	// Schema name should be quoted and include public
	expected := `SET LOCAL search_path TO "org_acme_bank", public`
	if mock.execQuery != expected {
		t.Errorf("Query = %q, want %q", mock.execQuery, expected)
	}
}

func TestWithOrganizationScope_MissingOrganizationContext(t *testing.T) {
	mock := &mockDB{}
	ctx := context.Background() // No organization in context

	result, err := db.WithOrganizationScope(ctx, mock)

	if err == nil {
		t.Fatal("WithOrganizationScope should return error for missing organization context")
	}
	if !errors.Is(err, organization.ErrMissingOrganizationContext) {
		t.Errorf("error = %v, want ErrMissingOrganizationContext", err)
	}
	if result != nil {
		t.Error("WithOrganizationScope should return nil DB on error")
	}
	if mock.execCalled {
		t.Error("WithOrganizationScope should not execute query when organization missing")
	}
}

func TestWithOrganizationScope_ExecError(t *testing.T) {
	mock := &mockDB{execErr: errMockExecFailed}
	orgID := organization.MustNewOrganizationID("test_org")
	ctx := organization.WithOrganization(context.Background(), orgID)

	result, err := db.WithOrganizationScope(ctx, mock)

	if err == nil {
		t.Fatal("WithOrganizationScope should return error when exec fails")
	}
	if !errors.Is(err, errMockExecFailed) {
		t.Errorf("error should wrap exec error, got: %v", err)
	}
	if result != nil {
		t.Error("WithOrganizationScope should return nil DB on error")
	}
}

func TestWithOrganizationScope_SchemaNameQuoting(t *testing.T) {
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
			orgID := organization.MustNewOrganizationID(tt.orgID)
			ctx := organization.WithOrganization(context.Background(), orgID)

			_, err := db.WithOrganizationScope(ctx, mock)
			if err != nil {
				t.Fatalf("WithOrganizationScope returned unexpected error: %v", err)
			}
			expected := "SET LOCAL search_path TO " + tt.expectedSchema + ", public"
			if mock.execQuery != expected {
				t.Errorf("Query = %q, want %q", mock.execQuery, expected)
			}
		})
	}
}

func TestMustWithOrganizationScope_Success(t *testing.T) {
	mock := &mockDB{}
	orgID := organization.MustNewOrganizationID("test_org")
	ctx := organization.WithOrganization(context.Background(), orgID)

	// Should not panic
	result := db.MustWithOrganizationScope(ctx, mock)

	if result != mock {
		t.Error("MustWithOrganizationScope should return the same DB instance")
	}
}

func TestMustWithOrganizationScope_Panics(t *testing.T) {
	mock := &mockDB{}
	ctx := context.Background() // No organization

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustWithOrganizationScope should panic when organization missing")
		}
	}()

	db.MustWithOrganizationScope(ctx, mock)
}
