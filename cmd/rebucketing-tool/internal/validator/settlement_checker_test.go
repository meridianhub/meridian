package validator

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockSettlementLockChecker_CheckSettlementLock(t *testing.T) {
	t.Run("returns nil when no settlement lock exists", func(t *testing.T) {
		checker := NewMockSettlementLockChecker(nil)

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		err := checker.CheckSettlementLock(ctx, "USD", 1)

		require.NoError(t, err)
	})

	t.Run("returns SettlementLockError when positions exist in finalized settlements", func(t *testing.T) {
		checker := NewMockSettlementLockCheckerWithLock("USD", 1, 5, []string{"settle-1", "settle-2"})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		err := checker.CheckSettlementLock(ctx, "USD", 1)

		require.Error(t, err)

		var lockErr *SettlementLockError
		require.True(t, errors.As(err, &lockErr))
		assert.Equal(t, "USD", lockErr.InstrumentCode)
		assert.Equal(t, 1, lockErr.InstrumentVersion)
		assert.Equal(t, 5, lockErr.PositionCount)
		assert.Equal(t, []string{"settle-1", "settle-2"}, lockErr.SettlementIDs)
	})

	t.Run("returns nil for different instrument when specific lock configured", func(t *testing.T) {
		checker := NewMockSettlementLockCheckerWithLock("USD", 1, 5, []string{"settle-1"})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		err := checker.CheckSettlementLock(ctx, "EUR", 1) // Different instrument

		require.NoError(t, err)
	})

	t.Run("returns nil for different version when specific lock configured", func(t *testing.T) {
		checker := NewMockSettlementLockCheckerWithLock("USD", 1, 5, []string{"settle-1"})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		err := checker.CheckSettlementLock(ctx, "USD", 2) // Different version

		require.NoError(t, err)
	})

	t.Run("returns error when tenant context is missing", func(t *testing.T) {
		checker := NewMockSettlementLockChecker(nil)

		ctx := context.Background() // No tenant context
		err := checker.CheckSettlementLock(ctx, "USD", 1)

		require.Error(t, err)
		assert.Equal(t, ErrMissingTenantContext, err)
	})

	t.Run("returns custom error when configured", func(t *testing.T) {
		checker := NewMockSettlementLockChecker(errServiceUnavailable)

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		err := checker.CheckSettlementLock(ctx, "USD", 1)

		require.Error(t, err)
		assert.Equal(t, errServiceUnavailable, err)
	})
}

func TestIsSettlementLocked(t *testing.T) {
	t.Run("returns true for SettlementLockError", func(t *testing.T) {
		err := &SettlementLockError{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
			PositionCount:     3,
			SettlementIDs:     []string{"s1"},
		}

		assert.True(t, IsSettlementLocked(err))
	})

	t.Run("returns false for nil error", func(t *testing.T) {
		assert.False(t, IsSettlementLocked(nil))
	})

	t.Run("returns false for other error types", func(t *testing.T) {
		assert.False(t, IsSettlementLocked(errServiceUnavailable))
		assert.False(t, IsSettlementLocked(&InstrumentNotFoundError{}))
	})

	t.Run("returns true for wrapped SettlementLockError", func(t *testing.T) {
		wrapped := &ValidationError{
			Operation: "test",
			Message:   "test",
			Cause: &SettlementLockError{
				InstrumentCode:    "USD",
				InstrumentVersion: 1,
			},
		}

		// Note: IsSettlementLocked uses errors.As, so this should work
		assert.True(t, IsSettlementLocked(wrapped))
	})
}

func TestSettlementLockChecker_Close(t *testing.T) {
	t.Run("mock Close returns nil by default", func(t *testing.T) {
		checker := NewMockSettlementLockChecker(nil)

		err := checker.Close()
		require.NoError(t, err)
	})

	t.Run("mock Close returns configured error", func(t *testing.T) {
		checker := &MockSettlementLockChecker{
			CloseFunc: func() error {
				return errCloseFailed
			},
		}

		err := checker.Close()
		require.Error(t, err)
		assert.Equal(t, errCloseFailed, err)
	})
}

func TestContainsInstrumentReference(t *testing.T) {
	t.Run("returns false for nil entry", func(t *testing.T) {
		result := containsInstrumentReference(nil, "USD", 1)
		assert.False(t, result)
	})
}

func TestContainsSubstring(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{"exact match", "hello", "hello", true},
		{"substring at start", "hello world", "hello", true},
		{"substring at end", "hello world", "world", true},
		{"substring in middle", "hello world", "lo wo", true},
		{"no match", "hello world", "xyz", false},
		{"empty substring", "hello", "", true},
		{"empty string", "", "hello", false},
		{"both empty", "", "", true},
		{"substr longer than s", "hi", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsSubstring(tt.s, tt.substr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSettlementCheckerConfig_ApplyDefaults(t *testing.T) {
	t.Run("applies all defaults to empty config", func(t *testing.T) {
		cfg := &SettlementCheckerConfig{}
		cfg.applyDefaults()

		assert.Equal(t, DefaultSettlementCheckerTimeout, cfg.Timeout)
		assert.Equal(t, DefaultNamespace, cfg.Namespace)
		assert.NotNil(t, cfg.Logger)
	})

	t.Run("preserves custom values", func(t *testing.T) {
		cfg := &SettlementCheckerConfig{
			Timeout:   60 * 1000000000, // 60s in nanoseconds
			Namespace: "production",
		}
		cfg.applyDefaults()

		assert.Equal(t, 60*1000000000, int(cfg.Timeout))
		assert.Equal(t, "production", cfg.Namespace)
	})
}

func TestMockSettlementLockChecker_Interface(t *testing.T) {
	t.Run("implements SettlementLockCheckerInterface", func(_ *testing.T) {
		var _ SettlementLockCheckerInterface = (*MockSettlementLockChecker)(nil)
	})

	t.Run("custom CheckFunc is called", func(t *testing.T) {
		called := false
		checker := &MockSettlementLockChecker{
			CheckFunc: func(_ context.Context, instrumentCode string, instrumentVersion int) error {
				called = true
				assert.Equal(t, "TEST", instrumentCode)
				assert.Equal(t, 42, instrumentVersion)
				return nil
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		_ = checker.CheckSettlementLock(ctx, "TEST", 42)

		assert.True(t, called)
	})
}
