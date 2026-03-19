package provisioner

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

func TestMaskDatabaseURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "URL with password gets masked",
			input:    "postgres://admin:secretpass@localhost:5432/mydb",
			expected: "postgres://admin:%2A%2A%2A@localhost:5432/mydb",
		},
		{
			name:     "URL without password unchanged",
			input:    "postgres://admin@localhost:5432/mydb",
			expected: "postgres://admin@localhost:5432/mydb",
		},
		{
			name:     "URL with no user info unchanged",
			input:    "postgres://localhost:5432/mydb",
			expected: "postgres://localhost:5432/mydb",
		},
		{
			name:     "empty string returned as-is",
			input:    "",
			expected: "",
		},
		{
			name:     "non-URL string returned as-is",
			input:    "not-a-url",
			expected: "not-a-url",
		},
		{
			name:     "URL with query params preserves params",
			input:    "postgres://user:pass@host:5432/db?sslmode=require",
			expected: "postgres://user:%2A%2A%2A@host:5432/db?sslmode=require",
		},
		{
			name:     "URL with empty password gets masked",
			input:    "postgres://user:@localhost:5432/db",
			expected: "postgres://user:%2A%2A%2A@localhost:5432/db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maskDatabaseURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	t.Run("nil error returns false", func(t *testing.T) {
		assert.False(t, isAlreadyExistsError(nil))
	})

	t.Run("PgError duplicate_table (42P07) returns true", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "42P07", Message: "relation already exists"}
		assert.True(t, isAlreadyExistsError(pgErr))
	})

	t.Run("PgError duplicate_schema (42P06) returns true", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "42P06", Message: "schema already exists"}
		assert.True(t, isAlreadyExistsError(pgErr))
	})

	t.Run("PgError duplicate_object (42710) returns true", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "42710", Message: "index already exists"}
		assert.True(t, isAlreadyExistsError(pgErr))
	})

	t.Run("PgError unique_violation (23505) returns false", func(t *testing.T) {
		pgErr := &pgconn.PgError{Code: "23505", Message: "unique constraint violated"}
		assert.False(t, isAlreadyExistsError(pgErr))
	})

	t.Run("error containing already exists returns true", func(t *testing.T) {
		err := errors.New("table already exists in schema")
		assert.True(t, isAlreadyExistsError(err))
	})

	t.Run("error containing ALREADY EXISTS uppercase returns true", func(t *testing.T) {
		err := errors.New("TABLE ALREADY EXISTS")
		assert.True(t, isAlreadyExistsError(err))
	})

	t.Run("error containing duplicate returns true", func(t *testing.T) {
		err := errors.New("duplicate key value")
		assert.True(t, isAlreadyExistsError(err))
	})

	t.Run("generic error returns false", func(t *testing.T) {
		err := errors.New("connection refused")
		assert.False(t, isAlreadyExistsError(err))
	})
}
