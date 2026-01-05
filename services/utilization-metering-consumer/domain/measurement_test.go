package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/pkg/platform/quantity"
)

// Test instruments mirror the production instruments from instruments.go.
// These are defined separately to avoid import cycles and to test instrument creation.
// See instruments.go for production instrument definitions.
var (
	transactionInstrument, _ = quantity.NewInstrument("TRANSACTION", 1, "COUNT", 0)
	apiCallInstrument, _     = quantity.NewInstrument("API_CALL", 1, "COUNT", 0)
	operationInstrument, _   = quantity.NewInstrument("OPERATION", 1, "COUNT", 0)
	// Note: precision matches production instruments (6 for fractional units)
	storageGBInstrument, _   = quantity.NewInstrument("STORAGE_GB_HOUR", 1, "DATA", 6)
	computeHourInstrument, _ = quantity.NewInstrument("COMPUTE_HOUR", 1, "COMPUTE", 6)
)

func TestUtilizationMeasurement_Creation(t *testing.T) {
	now := time.Now()

	measurement := &UtilizationMeasurement{
		TenantID:      "tenant-123",
		ServiceName:   "current-account",
		OperationType: "CreateAccount",
		Amount:        quantity.NewAssetFromInt(1, transactionInstrument),
		Timestamp:     now,
		CorrelationID: "corr-456",
	}

	assert.Equal(t, "tenant-123", measurement.TenantID)
	assert.Equal(t, "current-account", measurement.ServiceName)
	assert.Equal(t, "CreateAccount", measurement.OperationType)
	assert.Equal(t, int64(1), measurement.Amount.Amount.IntPart())
	assert.Equal(t, "TRANSACTION", measurement.Amount.Instrument.Code)
	assert.Equal(t, now, measurement.Timestamp)
	assert.Equal(t, "corr-456", measurement.CorrelationID)
}

func TestUtilizationMeasurement_DifferentQuantities(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
	}{
		{
			name:   "single transaction",
			amount: 1,
		},
		{
			name:   "batch of 10 operations",
			amount: 10,
		},
		{
			name:   "large batch",
			amount: 1000,
		},
		{
			name:   "zero quantity (edge case)",
			amount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      "tenant-test",
				ServiceName:   "test-service",
				OperationType: "TestOperation",
				Amount:        quantity.NewAssetFromInt(tt.amount, operationInstrument),
				Timestamp:     time.Now(),
				CorrelationID: "test-corr",
			}

			assert.Equal(t, tt.amount, measurement.Amount.Amount.IntPart())
		})
	}
}

func TestUtilizationMeasurement_DifferentInstrumentTypes(t *testing.T) {
	tests := []struct {
		name        string
		instrument  quantity.Instrument
		amount      int64
		description string
	}{
		{
			name:        "transaction count",
			instrument:  transactionInstrument,
			amount:      1,
			description: "Single transaction",
		},
		{
			name:        "API call count",
			instrument:  apiCallInstrument,
			amount:      5,
			description: "Multiple API calls",
		},
		{
			name:        "storage in gigabytes",
			instrument:  storageGBInstrument,
			amount:      100,
			description: "Storage usage",
		},
		{
			name:        "compute hours",
			instrument:  computeHourInstrument,
			amount:      24,
			description: "Compute resource usage",
		},
		{
			name:        "operation count",
			instrument:  operationInstrument,
			amount:      1,
			description: "Generic operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      "tenant-test",
				ServiceName:   "test-service",
				OperationType: "TestOperation",
				Amount:        quantity.NewAssetFromInt(tt.amount, tt.instrument),
				Timestamp:     time.Now(),
				CorrelationID: "test-corr",
			}

			assert.Equal(t, tt.instrument.Code, measurement.Amount.Instrument.Code)
			assert.Equal(t, tt.amount, measurement.Amount.Amount.IntPart())
		})
	}
}

func TestUtilizationMeasurement_AllServiceTypes(t *testing.T) {
	services := []string{
		"current-account",
		"financial-accounting",
		"position-keeping",
		"party",
		"payment-order",
		"tenant",
	}

	for _, service := range services {
		t.Run(service, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      "tenant-test",
				ServiceName:   service,
				OperationType: "TestOperation",
				Amount:        quantity.NewAssetFromInt(1, operationInstrument),
				Timestamp:     time.Now(),
				CorrelationID: "test-corr",
			}

			assert.Equal(t, service, measurement.ServiceName)
		})
	}
}

func TestUtilizationMeasurement_OperationTypes(t *testing.T) {
	tests := []struct {
		name          string
		operationType string
	}{
		{
			name:          "CREATE operation",
			operationType: "CreateAccount",
		},
		{
			name:          "UPDATE operation",
			operationType: "UpdateBalance",
		},
		{
			name:          "DELETE operation",
			operationType: "CloseAccount",
		},
		{
			name:          "READ operation",
			operationType: "GetAccountDetails",
		},
		{
			name:          "BATCH operation",
			operationType: "ProcessBatchPayments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      "tenant-test",
				ServiceName:   "test-service",
				OperationType: tt.operationType,
				Amount:        quantity.NewAssetFromInt(1, operationInstrument),
				Timestamp:     time.Now(),
				CorrelationID: "test-corr",
			}

			assert.Equal(t, tt.operationType, measurement.OperationType)
		})
	}
}

func TestUtilizationMeasurement_TenantIDs(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
	}{
		{
			name:     "UUID tenant ID",
			tenantID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:     "short alphanumeric tenant ID",
			tenantID: "tenant-123",
		},
		{
			name:     "organization name tenant ID",
			tenantID: "acme-corp",
		},
		{
			name:     "tenant-zero (platform operations)",
			tenantID: "tenant-zero",
		},
		{
			name:     "unknown tenant",
			tenantID: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      tt.tenantID,
				ServiceName:   "test-service",
				OperationType: "TestOperation",
				Amount:        quantity.NewAssetFromInt(1, operationInstrument),
				Timestamp:     time.Now(),
				CorrelationID: "test-corr",
			}

			assert.Equal(t, tt.tenantID, measurement.TenantID)
		})
	}
}

func TestUtilizationMeasurement_TimestampPrecision(t *testing.T) {
	now := time.Now()

	measurement := &UtilizationMeasurement{
		TenantID:      "tenant-test",
		ServiceName:   "test-service",
		OperationType: "TestOperation",
		Amount:        quantity.NewAssetFromInt(1, operationInstrument),
		Timestamp:     now,
		CorrelationID: "test-corr",
	}

	// Verify timestamp is preserved with full precision
	assert.Equal(t, now, measurement.Timestamp)
	assert.Equal(t, now.UnixNano(), measurement.Timestamp.UnixNano())
}

func TestUtilizationMeasurement_ZeroValueAmount(t *testing.T) {
	// Test that measurements can be created with zero-valued quantity
	// This might happen during testing or edge cases
	measurement := &UtilizationMeasurement{
		TenantID:      "tenant-test",
		ServiceName:   "test-service",
		OperationType: "TestOperation",
		Amount:        quantity.ZeroAsset(operationInstrument),
		Timestamp:     time.Now(),
		CorrelationID: "test-corr",
	}

	assert.True(t, measurement.Amount.IsZero())
	assert.Equal(t, "OPERATION", measurement.Amount.Instrument.Code)
}

func TestUtilizationMeasurement_EmptyFields(t *testing.T) {
	// Test that measurements can be created with empty/zero values
	// This might happen during testing or error scenarios
	measurement := &UtilizationMeasurement{
		TenantID:      "",
		ServiceName:   "",
		OperationType: "",
		Amount:        quantity.Asset{}, // zero-value Asset
		Timestamp:     time.Time{},
		CorrelationID: "",
	}

	assert.Equal(t, "", measurement.TenantID)
	assert.Equal(t, "", measurement.ServiceName)
	assert.Equal(t, "", measurement.OperationType)
	assert.True(t, measurement.Amount.IsZero())
	assert.True(t, measurement.Timestamp.IsZero())
	assert.Equal(t, "", measurement.CorrelationID)
}

func TestUtilizationMeasurement_CorrelationIDFormats(t *testing.T) {
	tests := []struct {
		name          string
		correlationID string
	}{
		{
			name:          "UUID format",
			correlationID: "550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:          "custom format with prefix",
			correlationID: "batch-2024-001-item-123",
		},
		{
			name:          "short ID",
			correlationID: "req-abc123",
		},
		{
			name:          "empty correlation ID",
			correlationID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      "tenant-test",
				ServiceName:   "test-service",
				OperationType: "TestOperation",
				Amount:        quantity.NewAssetFromInt(1, operationInstrument),
				Timestamp:     time.Now(),
				CorrelationID: tt.correlationID,
			}

			assert.Equal(t, tt.correlationID, measurement.CorrelationID)
		})
	}
}

// =============================================================================
// Quantity operations tests - verifying the Universal Asset System integration
// =============================================================================

func TestUtilizationMeasurement_QuantityOperations_Addition(t *testing.T) {
	// Test that quantities from the same instrument can be added
	amount1 := quantity.NewAssetFromInt(5, transactionInstrument)
	amount2 := quantity.NewAssetFromInt(3, transactionInstrument)

	result, err := amount1.Add(amount2)
	require.NoError(t, err)
	assert.Equal(t, int64(8), result.Amount.IntPart())
	assert.Equal(t, "TRANSACTION", result.Instrument.Code)
}

func TestUtilizationMeasurement_QuantityOperations_Comparison(t *testing.T) {
	// Test that quantities from the same instrument can be compared
	amount1 := quantity.NewAssetFromInt(5, transactionInstrument)
	amount2 := quantity.NewAssetFromInt(10, transactionInstrument)

	cmp, err := amount1.Compare(amount2)
	require.NoError(t, err)
	assert.Equal(t, -1, cmp) // amount1 < amount2

	lt, err := amount1.LessThan(amount2)
	require.NoError(t, err)
	assert.True(t, lt)
}

func TestUtilizationMeasurement_QuantityOperations_DifferentInstrumentsFail(t *testing.T) {
	// Test that quantities from different instruments cannot be combined
	transactionAmount := quantity.NewAssetFromInt(5, transactionInstrument)
	apiCallAmount := quantity.NewAssetFromInt(3, apiCallInstrument)

	_, err := transactionAmount.Add(apiCallAmount)
	require.Error(t, err)
	assert.ErrorIs(t, err, quantity.ErrInstrumentMismatch)
}

func TestUtilizationMeasurement_QuantityString(t *testing.T) {
	// Test the string representation of utilization quantities
	amount := quantity.NewAssetFromInt(42, transactionInstrument)
	assert.Equal(t, "42 TRANSACTION", amount.String())

	// Storage instrument uses 6 decimal places and full code name
	storageAmount := quantity.NewAssetFromInt(100, storageGBInstrument)
	assert.Equal(t, "100.000000 STORAGE_GB_HOUR", storageAmount.String())
}

func TestUtilizationMeasurement_InstrumentDimension(t *testing.T) {
	// Verify the instrument dimension is correctly set for utilization types
	assert.Equal(t, "COUNT", transactionInstrument.Dimension)
	assert.Equal(t, "COUNT", apiCallInstrument.Dimension)
	assert.Equal(t, "COUNT", operationInstrument.Dimension)
	assert.Equal(t, "DATA", storageGBInstrument.Dimension)
	assert.Equal(t, "COMPUTE", computeHourInstrument.Dimension)

	// All should be commodities, not currencies
	assert.True(t, transactionInstrument.IsCommodity())
	assert.True(t, apiCallInstrument.IsCommodity())
	assert.True(t, storageGBInstrument.IsCommodity())
	assert.False(t, transactionInstrument.IsMonetary())
}

func TestUtilizationMeasurement_InstrumentPrecision(t *testing.T) {
	// Verify precision for different utilization types matches production instruments
	assert.Equal(t, 0, transactionInstrument.Precision) // Whole transactions (COUNT)
	assert.Equal(t, 0, apiCallInstrument.Precision)     // Whole API calls (COUNT)
	assert.Equal(t, 6, storageGBInstrument.Precision)   // Six decimal places for storage (DATA)
	assert.Equal(t, 6, computeHourInstrument.Precision) // Six decimal places for compute time (COMPUTE)
}
