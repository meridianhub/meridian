package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUtilizationMeasurement_Creation(t *testing.T) {
	now := time.Now()

	measurement := &UtilizationMeasurement{
		TenantID:      "tenant-123",
		ServiceName:   "current-account",
		OperationType: "CreateAccount",
		Quantity:      1,
		UnitOfMeasure: "transaction",
		Timestamp:     now,
		CorrelationID: "corr-456",
	}

	assert.Equal(t, "tenant-123", measurement.TenantID)
	assert.Equal(t, "current-account", measurement.ServiceName)
	assert.Equal(t, "CreateAccount", measurement.OperationType)
	assert.Equal(t, int64(1), measurement.Quantity)
	assert.Equal(t, "transaction", measurement.UnitOfMeasure)
	assert.Equal(t, now, measurement.Timestamp)
	assert.Equal(t, "corr-456", measurement.CorrelationID)
}

func TestUtilizationMeasurement_DifferentQuantities(t *testing.T) {
	tests := []struct {
		name     string
		quantity int64
	}{
		{
			name:     "single transaction",
			quantity: 1,
		},
		{
			name:     "batch of 10 operations",
			quantity: 10,
		},
		{
			name:     "large batch",
			quantity: 1000,
		},
		{
			name:     "zero quantity (edge case)",
			quantity: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      "tenant-test",
				ServiceName:   "test-service",
				OperationType: "TestOperation",
				Quantity:      tt.quantity,
				UnitOfMeasure: "operation",
				Timestamp:     time.Now(),
				CorrelationID: "test-corr",
			}

			assert.Equal(t, tt.quantity, measurement.Quantity)
		})
	}
}

func TestUtilizationMeasurement_DifferentUnitsOfMeasure(t *testing.T) {
	tests := []struct {
		name          string
		unitOfMeasure string
		quantity      int64
		description   string
	}{
		{
			name:          "transaction count",
			unitOfMeasure: "transaction",
			quantity:      1,
			description:   "Single transaction",
		},
		{
			name:          "API call count",
			unitOfMeasure: "api_call",
			quantity:      5,
			description:   "Multiple API calls",
		},
		{
			name:          "storage in gigabytes",
			unitOfMeasure: "storage_gb",
			quantity:      100,
			description:   "Storage usage",
		},
		{
			name:          "compute hours",
			unitOfMeasure: "compute_hours",
			quantity:      24,
			description:   "Compute resource usage",
		},
		{
			name:          "operation count",
			unitOfMeasure: "operation",
			quantity:      1,
			description:   "Generic operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			measurement := &UtilizationMeasurement{
				TenantID:      "tenant-test",
				ServiceName:   "test-service",
				OperationType: "TestOperation",
				Quantity:      tt.quantity,
				UnitOfMeasure: tt.unitOfMeasure,
				Timestamp:     time.Now(),
				CorrelationID: "test-corr",
			}

			assert.Equal(t, tt.unitOfMeasure, measurement.UnitOfMeasure)
			assert.Equal(t, tt.quantity, measurement.Quantity)
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
				Quantity:      1,
				UnitOfMeasure: "operation",
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
				Quantity:      1,
				UnitOfMeasure: "operation",
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
				Quantity:      1,
				UnitOfMeasure: "operation",
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
		Quantity:      1,
		UnitOfMeasure: "operation",
		Timestamp:     now,
		CorrelationID: "test-corr",
	}

	// Verify timestamp is preserved with full precision
	assert.Equal(t, now, measurement.Timestamp)
	assert.Equal(t, now.UnixNano(), measurement.Timestamp.UnixNano())
}

func TestUtilizationMeasurement_EmptyFields(t *testing.T) {
	// Test that measurements can be created with empty/zero values
	// This might happen during testing or error scenarios
	measurement := &UtilizationMeasurement{
		TenantID:      "",
		ServiceName:   "",
		OperationType: "",
		Quantity:      0,
		UnitOfMeasure: "",
		Timestamp:     time.Time{},
		CorrelationID: "",
	}

	assert.Equal(t, "", measurement.TenantID)
	assert.Equal(t, "", measurement.ServiceName)
	assert.Equal(t, "", measurement.OperationType)
	assert.Equal(t, int64(0), measurement.Quantity)
	assert.Equal(t, "", measurement.UnitOfMeasure)
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
				Quantity:      1,
				UnitOfMeasure: "operation",
				Timestamp:     time.Now(),
				CorrelationID: tt.correlationID,
			}

			assert.Equal(t, tt.correlationID, measurement.CorrelationID)
		})
	}
}
