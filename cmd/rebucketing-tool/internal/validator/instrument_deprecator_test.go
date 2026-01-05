package validator

import (
	"context"
	"errors"
	"testing"
	"time"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMockInstrumentDeprecator_DeprecateInstrument(t *testing.T) {
	t.Run("returns nil when deprecation succeeds", func(t *testing.T) {
		deprecator := &MockInstrumentDeprecator{
			DeprecateFunc: func(ctx context.Context, req DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error) {
				if _, ok := tenant.FromContext(ctx); !ok {
					return nil, ErrMissingTenantContext
				}
				return &DeprecateInstrumentResponse{
					Instrument: &referencedatav1.InstrumentDefinition{
						Code:         req.InstrumentCode,
						Version:      int32(req.InstrumentVersion),
						Status:       referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,
						DeprecatedAt: timestamppb.Now(),
					},
				}, nil
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		resp, err := deprecator.DeprecateInstrument(ctx, DeprecateInstrumentRequest{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Instrument)
		assert.Equal(t, "USD", resp.Instrument.Code)
		assert.Equal(t, int32(1), resp.Instrument.Version)
		assert.Equal(t, referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED, resp.Instrument.Status)
	})

	t.Run("returns InstrumentAlreadyDeprecatedError when already deprecated", func(t *testing.T) {
		deprecator := &MockInstrumentDeprecator{
			DeprecateFunc: func(ctx context.Context, req DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error) {
				if _, ok := tenant.FromContext(ctx); !ok {
					return nil, ErrMissingTenantContext
				}
				return nil, &InstrumentAlreadyDeprecatedError{
					InstrumentCode:    req.InstrumentCode,
					InstrumentVersion: req.InstrumentVersion,
				}
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		resp, err := deprecator.DeprecateInstrument(ctx, DeprecateInstrumentRequest{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
		})

		require.Error(t, err)
		assert.Nil(t, resp)
		assert.True(t, IsInstrumentDeprecated(err))

		var deprecatedErr *InstrumentAlreadyDeprecatedError
		require.True(t, errors.As(err, &deprecatedErr))
		assert.Equal(t, "USD", deprecatedErr.InstrumentCode)
	})

	t.Run("returns InstrumentNotFoundError when instrument does not exist", func(t *testing.T) {
		deprecator := NewMockInstrumentDeprecatorWithError(&InstrumentNotFoundError{
			InstrumentCode:    "UNKNOWN",
			InstrumentVersion: 99,
		})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		resp, err := deprecator.DeprecateInstrument(ctx, DeprecateInstrumentRequest{
			InstrumentCode:    "UNKNOWN",
			InstrumentVersion: 99,
		})

		require.Error(t, err)
		assert.Nil(t, resp)
		assert.True(t, IsInstrumentNotFound(err))
	})

	t.Run("returns error when tenant context is missing", func(t *testing.T) {
		deprecator := NewMockInstrumentDeprecatorWithError(nil)

		ctx := context.Background() // No tenant context
		resp, err := deprecator.DeprecateInstrument(ctx, DeprecateInstrumentRequest{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
		})

		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, ErrMissingTenantContext, err)
	})

	t.Run("handles successor ID in request", func(t *testing.T) {
		var capturedSuccessorID string
		deprecator := &MockInstrumentDeprecator{
			DeprecateFunc: func(_ context.Context, req DeprecateInstrumentRequest) (*DeprecateInstrumentResponse, error) {
				capturedSuccessorID = req.SuccessorID
				return &DeprecateInstrumentResponse{
					Instrument: &referencedatav1.InstrumentDefinition{
						Code:        req.InstrumentCode,
						Version:     int32(req.InstrumentVersion),
						Status:      referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,
						SuccessorId: req.SuccessorID,
					},
				}, nil
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		_, err := deprecator.DeprecateInstrument(ctx, DeprecateInstrumentRequest{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
			SuccessorID:       "550e8400-e29b-41d4-a716-446655440000",
		})

		require.NoError(t, err)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", capturedSuccessorID)
	})
}

func TestMockInstrumentDeprecator_RetrieveInstrument(t *testing.T) {
	t.Run("returns instrument when it exists", func(t *testing.T) {
		deprecator := &MockInstrumentDeprecator{
			RetrieveFunc: func(ctx context.Context, code string, version int) (*referencedatav1.InstrumentDefinition, error) {
				if _, ok := tenant.FromContext(ctx); !ok {
					return nil, ErrMissingTenantContext
				}
				return &referencedatav1.InstrumentDefinition{
					Code:      code,
					Version:   int32(version),
					Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
					Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
					Precision: 2,
				}, nil
			},
		}

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		instrument, err := deprecator.RetrieveInstrument(ctx, "USD", 1)

		require.NoError(t, err)
		require.NotNil(t, instrument)
		assert.Equal(t, "USD", instrument.Code)
		assert.Equal(t, int32(1), instrument.Version)
		assert.Equal(t, referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE, instrument.Status)
	})

	t.Run("returns error when instrument not found", func(t *testing.T) {
		deprecator := NewMockInstrumentDeprecatorWithError(&InstrumentNotFoundError{
			InstrumentCode:    "UNKNOWN",
			InstrumentVersion: 99,
		})

		ctx := tenant.WithTenant(context.Background(), "test-tenant")
		instrument, err := deprecator.RetrieveInstrument(ctx, "UNKNOWN", 99)

		require.Error(t, err)
		assert.Nil(t, instrument)
		assert.True(t, IsInstrumentNotFound(err))
	})
}

func TestIsInstrumentDeprecated(t *testing.T) {
	t.Run("returns true for InstrumentAlreadyDeprecatedError", func(t *testing.T) {
		err := &InstrumentAlreadyDeprecatedError{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
		}
		assert.True(t, IsInstrumentDeprecated(err))
	})

	t.Run("returns false for nil error", func(t *testing.T) {
		assert.False(t, IsInstrumentDeprecated(nil))
	})

	t.Run("returns false for other error types", func(t *testing.T) {
		assert.False(t, IsInstrumentDeprecated(errServiceUnavailable))
		assert.False(t, IsInstrumentDeprecated(&InstrumentNotFoundError{}))
	})

	t.Run("returns true for wrapped error", func(t *testing.T) {
		wrapped := &ValidationError{
			Operation: "test",
			Message:   "test",
			Cause: &InstrumentAlreadyDeprecatedError{
				InstrumentCode:    "USD",
				InstrumentVersion: 1,
			},
		}
		assert.True(t, IsInstrumentDeprecated(wrapped))
	})
}

func TestIsInstrumentNotFound(t *testing.T) {
	t.Run("returns true for InstrumentNotFoundError", func(t *testing.T) {
		err := &InstrumentNotFoundError{
			InstrumentCode:    "UNKNOWN",
			InstrumentVersion: 99,
		}
		assert.True(t, IsInstrumentNotFound(err))
	})

	t.Run("returns false for nil error", func(t *testing.T) {
		assert.False(t, IsInstrumentNotFound(nil))
	})

	t.Run("returns false for other error types", func(t *testing.T) {
		assert.False(t, IsInstrumentNotFound(errServiceUnavailable))
		assert.False(t, IsInstrumentNotFound(&InstrumentAlreadyDeprecatedError{}))
	})

	t.Run("returns true for wrapped error", func(t *testing.T) {
		wrapped := &ValidationError{
			Operation: "test",
			Message:   "test",
			Cause: &InstrumentNotFoundError{
				InstrumentCode:    "UNKNOWN",
				InstrumentVersion: 99,
			},
		}
		assert.True(t, IsInstrumentNotFound(wrapped))
	})
}

func TestInstrumentDeprecatorConfig_ApplyDefaults(t *testing.T) {
	t.Run("applies all defaults to empty config", func(t *testing.T) {
		cfg := &InstrumentDeprecatorConfig{}
		cfg.applyDefaults()

		assert.Equal(t, DefaultDeprecatorTimeout, cfg.Timeout)
		assert.Equal(t, DefaultNamespace, cfg.Namespace)
		assert.Equal(t, DefaultReferenceDataPort, cfg.Port)
		assert.NotNil(t, cfg.Logger)
	})

	t.Run("preserves custom values", func(t *testing.T) {
		customTimeout := 60 * time.Second
		cfg := &InstrumentDeprecatorConfig{
			Timeout:   customTimeout,
			Namespace: "production",
			Port:      9999,
		}
		cfg.applyDefaults()

		assert.Equal(t, customTimeout, cfg.Timeout)
		assert.Equal(t, "production", cfg.Namespace)
		assert.Equal(t, 9999, cfg.Port)
	})
}

func TestMockInstrumentDeprecator_Close(t *testing.T) {
	t.Run("returns nil by default", func(t *testing.T) {
		deprecator := NewMockInstrumentDeprecator()

		err := deprecator.Close()
		require.NoError(t, err)
	})

	t.Run("returns configured error", func(t *testing.T) {
		deprecator := &MockInstrumentDeprecator{
			CloseFunc: func() error {
				return errCloseFailed
			},
		}

		err := deprecator.Close()
		require.Error(t, err)
		assert.Equal(t, errCloseFailed, err)
	})
}

func TestMockInstrumentDeprecator_Interface(t *testing.T) {
	t.Run("implements InstrumentDeprecatorInterface", func(_ *testing.T) {
		var _ InstrumentDeprecatorInterface = (*MockInstrumentDeprecator)(nil)
	})
}
