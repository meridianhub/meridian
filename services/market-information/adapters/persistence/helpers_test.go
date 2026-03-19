package persistence

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
)

func TestGetUserFromContext(t *testing.T) {
	t.Run("returns system for nil context", func(t *testing.T) {
		result := getUserFromContext(nil) //nolint:staticcheck // testing nil context behavior
		assert.Equal(t, "system", result)
	})

	t.Run("returns system for context without user", func(t *testing.T) {
		result := getUserFromContext(context.Background())
		assert.Equal(t, "system", result)
	})

	t.Run("returns user ID from context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-42")
		result := getUserFromContext(ctx)
		assert.Equal(t, "user-42", result)
	})

	t.Run("returns system for empty user ID in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "")
		result := getUserFromContext(ctx)
		assert.Equal(t, "system", result)
	})
}

func TestNullStringPtr(t *testing.T) {
	t.Run("returns nil for invalid NullString", func(t *testing.T) {
		ns := sql.NullString{Valid: false}
		assert.Nil(t, nullStringPtr(ns))
	})

	t.Run("returns string for valid NullString", func(t *testing.T) {
		ns := sql.NullString{String: "hello", Valid: true}
		assert.Equal(t, "hello", nullStringPtr(ns))
	})
}

func TestNullTimePtr(t *testing.T) {
	t.Run("returns nil for invalid NullTime", func(t *testing.T) {
		nt := sql.NullTime{Valid: false}
		assert.Nil(t, nullTimePtr(nt))
	})

	t.Run("returns time for valid NullTime", func(t *testing.T) {
		now := time.Now()
		nt := sql.NullTime{Time: now, Valid: true}
		assert.Equal(t, now, nullTimePtr(nt))
	})
}

func TestNullTimeValue(t *testing.T) {
	t.Run("returns invalid for zero time", func(t *testing.T) {
		result := nullTimeValue(time.Time{})
		assert.False(t, result.Valid)
	})

	t.Run("returns valid for non-zero time", func(t *testing.T) {
		now := time.Now()
		result := nullTimeValue(now)
		assert.True(t, result.Valid)
		assert.Equal(t, now, result.Time)
	})
}

func TestNullUUIDValue(t *testing.T) {
	t.Run("returns invalid for nil pointer", func(t *testing.T) {
		result := nullUUIDValue(nil)
		assert.False(t, result.Valid)
	})

	t.Run("returns valid for non-nil pointer", func(t *testing.T) {
		id := uuid.New()
		result := nullUUIDValue(&id)
		assert.True(t, result.Valid)
		assert.Equal(t, id, result.UUID)
	})
}

func TestNullUUIDNonNil(t *testing.T) {
	t.Run("returns invalid for nil UUID", func(t *testing.T) {
		result := nullUUIDNonNil(uuid.Nil)
		assert.False(t, result.Valid)
	})

	t.Run("returns valid for non-nil UUID", func(t *testing.T) {
		id := uuid.New()
		result := nullUUIDNonNil(id)
		assert.True(t, result.Valid)
		assert.Equal(t, id, result.UUID)
	})
}
