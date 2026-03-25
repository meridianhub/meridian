package validation

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
)

func TestDefaultInstrumentCheckerConfig(t *testing.T) {
	cfg := DefaultInstrumentCheckerConfig()

	assert.Equal(t, "reference-data", cfg.ServiceName)
	assert.Equal(t, "default", cfg.Namespace)
	assert.Equal(t, 50051, cfg.Port)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, 1000, cfg.CacheSize)
	assert.Equal(t, 5*time.Minute, cfg.CacheTTL)
	assert.Empty(t, cfg.Target)
}

func TestApplyInstrumentDefaults(t *testing.T) {
	t.Run("fills zero values with defaults", func(t *testing.T) {
		cfg := InstrumentCheckerConfig{}
		applyInstrumentDefaults(&cfg)

		assert.Equal(t, 30*time.Second, cfg.Timeout)
		assert.Equal(t, "default", cfg.Namespace)
		assert.Equal(t, 50051, cfg.Port)
		assert.Equal(t, 1000, cfg.CacheSize)
		assert.Equal(t, 5*time.Minute, cfg.CacheTTL)
		assert.NotNil(t, cfg.Logger)
	})

	t.Run("preserves existing values", func(t *testing.T) {
		cfg := InstrumentCheckerConfig{
			Timeout:   60 * time.Second,
			Namespace: "custom",
			Port:      9090,
			CacheSize: 500,
			CacheTTL:  10 * time.Minute,
		}
		applyInstrumentDefaults(&cfg)

		assert.Equal(t, 60*time.Second, cfg.Timeout)
		assert.Equal(t, "custom", cfg.Namespace)
		assert.Equal(t, 9090, cfg.Port)
		assert.Equal(t, 500, cfg.CacheSize)
		assert.Equal(t, 10*time.Minute, cfg.CacheTTL)
	})
}

func TestCreateInstrumentConnection_NoTargetOrServiceName(t *testing.T) {
	cfg := InstrumentCheckerConfig{
		Target:      "",
		ServiceName: "",
	}

	_, err := createInstrumentConnection(context.Background(), cfg)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInstrumentConfigInvalid))
}

func TestCreateInstrumentConnection_WithTarget(t *testing.T) {
	cfg := InstrumentCheckerConfig{
		Target: "localhost:50051",
	}

	conn, err := createInstrumentConnection(context.Background(), cfg)
	// grpc.NewClient does not immediately connect, so no error expected
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())
}

func TestCacheKey(t *testing.T) {
	t.Run("version 0 uses latest", func(t *testing.T) {
		assert.Equal(t, "GBPUSD:latest", cacheKey("GBPUSD", 0))
	})

	t.Run("non-zero version uses version number", func(t *testing.T) {
		assert.Equal(t, "GBPUSD:3", cacheKey("GBPUSD", 3))
	})

	t.Run("different codes produce different keys", func(t *testing.T) {
		k1 := cacheKey("GBPUSD", 1)
		k2 := cacheKey("EURUSD", 1)
		assert.NotEqual(t, k1, k2)
	})
}

func TestMockInstrumentChecker_Check(t *testing.T) {
	activeInstrument := &referencedatav1.InstrumentDefinition{
		Code:   "GBPUSD",
		Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
	}

	mock := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"GBPUSD": activeInstrument,
		},
	}

	t.Run("returns existing instrument", func(t *testing.T) {
		result, err := mock.Check(context.Background(), "GBPUSD", 0)
		require.NoError(t, err)
		assert.True(t, result.Exists)
		assert.True(t, result.IsActive)
		assert.Equal(t, activeInstrument, result.Definition)
	})

	t.Run("returns not found for unknown instrument", func(t *testing.T) {
		result, err := mock.Check(context.Background(), "UNKNOWN", 0)
		require.NoError(t, err)
		assert.False(t, result.Exists)
	})
}

func TestMockInstrumentChecker_CheckFunc(t *testing.T) {
	mock := &MockInstrumentChecker{
		CheckFunc: func(_ context.Context, code string, _ int) (*InstrumentCheckResult, error) {
			if code == "ERROR" {
				return nil, fmt.Errorf("forced error")
			}
			return &InstrumentCheckResult{Exists: true, IsActive: true}, nil
		},
	}

	result, err := mock.Check(context.Background(), "GBPUSD", 0)
	require.NoError(t, err)
	assert.True(t, result.Exists)

	_, err = mock.Check(context.Background(), "ERROR", 0)
	assert.Error(t, err)
}

func TestMockInstrumentChecker_CheckBatch(t *testing.T) {
	mock := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"GBPUSD": {Code: "GBPUSD", Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE},
			"EURUSD": {Code: "EURUSD", Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT},
		},
	}

	results, err := mock.CheckBatch(context.Background(), []string{"GBPUSD", "EURUSD", "UNKNOWN"}, 0)
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.True(t, results["GBPUSD"].Exists)
	assert.True(t, results["GBPUSD"].IsActive)
	assert.True(t, results["EURUSD"].Exists)
	assert.False(t, results["EURUSD"].IsActive)
	assert.False(t, results["UNKNOWN"].Exists)
}

func TestMockInstrumentChecker_GetAttributeSchema(t *testing.T) {
	mock := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"GBPUSD": {Code: "GBPUSD", AttributeSchema: `{"type":"object"}`},
		},
	}

	schema, err := mock.GetAttributeSchema(context.Background(), "GBPUSD", 0)
	require.NoError(t, err)
	assert.Equal(t, `{"type":"object"}`, schema)

	_, err = mock.GetAttributeSchema(context.Background(), "UNKNOWN", 0)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInstrumentNotFound))
}

func TestMockInstrumentChecker_Stats(t *testing.T) {
	mock := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"A": {Code: "A"},
			"B": {Code: "B"},
		},
	}

	stats := mock.Stats()
	assert.Equal(t, int64(0), stats.Hits)
	assert.Equal(t, int64(0), stats.Misses)
	assert.Equal(t, 2, stats.Size)
}

func TestMockInstrumentChecker_Close(t *testing.T) {
	mock := &MockInstrumentChecker{}
	assert.NoError(t, mock.Close())
}

func TestInstrumentCheckResult(t *testing.T) {
	result := &InstrumentCheckResult{
		Exists:     true,
		IsActive:   false,
		WasCreated: true,
	}
	assert.True(t, result.Exists)
	assert.False(t, result.IsActive)
	assert.True(t, result.WasCreated)
}

func TestInstrumentErrors(t *testing.T) {
	assert.NotNil(t, ErrInstrumentConfigInvalid)
	assert.NotNil(t, ErrInstrumentNotFound)
	assert.NotNil(t, ErrInstrumentNotActive)
}
