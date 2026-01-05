package validator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockPrerequisitesChecker_CheckPrerequisites(t *testing.T) {
	t.Run("returns success result when prerequisites are met", func(t *testing.T) {
		checker := NewMockPrerequisitesCheckerWithMeasurements(100, []string{"account-1", "account-2"})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		result, err := checker.CheckPrerequisites(ctx, "USD", 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.MeasurementsAvailable)
		assert.Equal(t, 100, result.MeasurementCount)
		assert.Equal(t, []string{"account-1", "account-2"}, result.AffectedAccountIDs)
	})

	t.Run("returns RawMeasurementsUnavailableError when no measurements", func(t *testing.T) {
		checker := NewMockPrerequisitesCheckerWithError(&RawMeasurementsUnavailableError{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
			Reason:            "no raw measurements found",
		})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		result, err := checker.CheckPrerequisites(ctx, "USD", 1)

		require.Error(t, err)
		assert.Nil(t, result)

		var unavailableErr *RawMeasurementsUnavailableError
		require.True(t, errors.As(err, &unavailableErr))
		assert.Equal(t, "USD", unavailableErr.InstrumentCode)
		assert.Equal(t, 1, unavailableErr.InstrumentVersion)
	})

	t.Run("returns error when tenant context is missing", func(t *testing.T) {
		checker := NewMockPrerequisitesCheckerWithMeasurements(100, nil)

		ctx := context.Background() // No tenant context
		result, err := checker.CheckPrerequisites(ctx, "USD", 1)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, ErrMissingTenantContext, err)
	})

	t.Run("returns result with zero measurements when none found", func(t *testing.T) {
		checker := NewMockPrerequisitesCheckerWithMeasurements(0, []string{})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		result, err := checker.CheckPrerequisites(ctx, "USD", 1)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.MeasurementsAvailable)
		assert.Equal(t, 0, result.MeasurementCount)
		assert.Empty(t, result.AffectedAccountIDs)
	})

	t.Run("returns custom error when configured", func(t *testing.T) {
		checker := NewMockPrerequisitesCheckerWithError(errServiceUnavailable)

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		result, err := checker.CheckPrerequisites(ctx, "USD", 1)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, errServiceUnavailable, err)
	})
}

func TestMockPrerequisitesChecker_ValidateInstrumentNotInUse(t *testing.T) {
	t.Run("returns nil when instrument is not in use", func(t *testing.T) {
		checker := NewMockPrerequisitesChecker()

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		err := checker.ValidateInstrumentNotInUse(ctx, "USD", 1, 24*time.Hour)

		require.NoError(t, err)
	})

	t.Run("returns InstrumentInUseError when instrument is in active use", func(t *testing.T) {
		checker := &MockPrerequisitesChecker{
			NotInUseFunc: func(ctx context.Context, instrumentCode string, instrumentVersion int, _ time.Duration) error {
				if _, ok := tenant.FromContext(ctx); !ok {
					return ErrMissingTenantContext
				}
				return &InstrumentInUseError{
					InstrumentCode:    instrumentCode,
					InstrumentVersion: instrumentVersion,
					ActiveTradeCount:  15,
				}
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		err := checker.ValidateInstrumentNotInUse(ctx, "USD", 1, 24*time.Hour)

		require.Error(t, err)

		var inUseErr *InstrumentInUseError
		require.True(t, errors.As(err, &inUseErr))
		assert.Equal(t, "USD", inUseErr.InstrumentCode)
		assert.Equal(t, 1, inUseErr.InstrumentVersion)
		assert.Equal(t, 15, inUseErr.ActiveTradeCount)
	})

	t.Run("respects lookback duration parameter", func(t *testing.T) {
		var capturedLookback time.Duration
		checker := &MockPrerequisitesChecker{
			NotInUseFunc: func(_ context.Context, _ string, _ int, lookbackDuration time.Duration) error {
				capturedLookback = lookbackDuration
				return nil
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		expectedLookback := 48 * time.Hour
		_ = checker.ValidateInstrumentNotInUse(ctx, "USD", 1, expectedLookback)

		assert.Equal(t, expectedLookback, capturedLookback)
	})
}

func TestPrerequisitesCheckResult(t *testing.T) {
	t.Run("can hold multiple affected account IDs", func(t *testing.T) {
		result := &PrerequisitesCheckResult{
			MeasurementsAvailable: true,
			MeasurementCount:      500,
			AffectedAccountIDs:    []string{"acc-1", "acc-2", "acc-3", "acc-4", "acc-5"},
			Warnings:              []string{"some warning"},
		}

		assert.Len(t, result.AffectedAccountIDs, 5)
		assert.Len(t, result.Warnings, 1)
	})

	t.Run("can hold multiple warnings", func(t *testing.T) {
		result := &PrerequisitesCheckResult{
			MeasurementsAvailable: true,
			MeasurementCount:      100,
			Warnings: []string{
				"warning 1: some issue",
				"warning 2: another issue",
				"warning 3: yet another issue",
			},
		}

		assert.Len(t, result.Warnings, 3)
	})
}

func TestPrerequisitesCheckerConfig_ApplyDefaults(t *testing.T) {
	t.Run("applies all defaults to empty config", func(t *testing.T) {
		cfg := &PrerequisitesCheckerConfig{}
		cfg.applyDefaults()

		assert.Equal(t, DefaultPrerequisitesTimeout, cfg.Timeout)
		assert.Equal(t, DefaultNamespace, cfg.Namespace)
		assert.NotNil(t, cfg.Logger)
	})

	t.Run("preserves custom values", func(t *testing.T) {
		customTimeout := 60 * time.Second
		cfg := &PrerequisitesCheckerConfig{
			Timeout:   customTimeout,
			Namespace: "production",
		}
		cfg.applyDefaults()

		assert.Equal(t, customTimeout, cfg.Timeout)
		assert.Equal(t, "production", cfg.Namespace)
	})
}

func TestMockPrerequisitesChecker_Close(t *testing.T) {
	t.Run("returns nil by default", func(t *testing.T) {
		checker := NewMockPrerequisitesChecker()

		err := checker.Close()
		require.NoError(t, err)
	})

	t.Run("returns configured error", func(t *testing.T) {
		checker := &MockPrerequisitesChecker{
			CloseFunc: func() error {
				return errCloseFailed
			},
		}

		err := checker.Close()
		require.Error(t, err)
		assert.Equal(t, errCloseFailed, err)
	})
}

func TestMockPrerequisitesChecker_Interface(t *testing.T) {
	t.Run("implements PrerequisitesCheckerInterface", func(_ *testing.T) {
		var _ PrerequisitesCheckerInterface = (*MockPrerequisitesChecker)(nil)
	})
}

func TestRawMeasurementsUnavailableError_Variants(t *testing.T) {
	testCases := []struct {
		name   string
		reason string
	}{
		{
			name:   "measurements archived",
			reason: "measurements archived beyond 90-day retention period",
		},
		{
			name:   "no measurements recorded",
			reason: "no measurements found for 5 positions",
		},
		{
			name:   "measurement store unavailable",
			reason: "measurement store returned timeout after 30s",
		},
		{
			name:   "data corruption",
			reason: "measurement checksums do not match",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := &RawMeasurementsUnavailableError{
				InstrumentCode:    "KWH",
				InstrumentVersion: 2,
				Reason:            tc.reason,
			}

			msg := err.Error()
			assert.Contains(t, msg, "KWH")
			assert.Contains(t, msg, "v2")
			assert.Contains(t, msg, tc.reason)
		})
	}
}

func TestPrerequisitesIntegration(t *testing.T) {
	t.Run("full validation flow with mock", func(t *testing.T) {
		// Simulate a full validation flow
		checker := &MockPrerequisitesChecker{
			CheckFunc: func(ctx context.Context, instrumentCode string, instrumentVersion int) (*PrerequisitesCheckResult, error) {
				if _, ok := tenant.FromContext(ctx); !ok {
					return nil, ErrMissingTenantContext
				}

				// Simulate finding positions with measurements
				if instrumentCode == "USD" && instrumentVersion == 1 {
					return &PrerequisitesCheckResult{
						MeasurementsAvailable: true,
						MeasurementCount:      250,
						AffectedAccountIDs:    []string{"acc-001", "acc-002", "acc-003"},
						Warnings:              nil,
					}, nil
				}

				// Simulate no measurements for other instruments
				return nil, &RawMeasurementsUnavailableError{
					InstrumentCode:    instrumentCode,
					InstrumentVersion: instrumentVersion,
					Reason:            "no positions found with this instrument",
				}
			},
			NotInUseFunc: func(ctx context.Context, _ string, _ int, _ time.Duration) error {
				if _, ok := tenant.FromContext(ctx); !ok {
					return ErrMissingTenantContext
				}

				// Simulate no active use
				return nil
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")

		// Test successful case
		result, err := checker.CheckPrerequisites(ctx, "USD", 1)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.MeasurementsAvailable)
		assert.Equal(t, 250, result.MeasurementCount)
		assert.Len(t, result.AffectedAccountIDs, 3)

		// Test active use check
		err = checker.ValidateInstrumentNotInUse(ctx, "USD", 1, 24*time.Hour)
		require.NoError(t, err)

		// Test failure case - different instrument
		_, err = checker.CheckPrerequisites(ctx, "EUR", 1)
		require.Error(t, err)

		var unavailableErr *RawMeasurementsUnavailableError
		require.True(t, errors.As(err, &unavailableErr))
	})
}
