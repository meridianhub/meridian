// Package domain contains the core domain models for utilization metering.
package domain

import (
	"time"

	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// UtilizationMeasurement represents a single utilization measurement derived from an audit event.
// This will be sent to Position Keeping service as a position change for tenant-zero billing.
type UtilizationMeasurement struct {
	// TenantID is the tenant that generated the utilization (customer being billed)
	TenantID string

	// ServiceName is the Meridian service that generated the audit event
	// Examples: "current-account", "payment-order", "financial-accounting"
	ServiceName string

	// OperationType is the operation that was performed (e.g., "CreateAccount", "ProcessPayment")
	OperationType string

	// Amount is the measured quantity for billing purposes using the Universal Asset System.
	// The instrument identifies what is being measured (TRANSACTION, API_CALL, STORAGE_GB, etc.)
	// Examples: 1 TRANSACTION, 5 API_CALL, 100 STORAGE_GB
	Amount quantity.Asset

	// Timestamp is when the utilization occurred
	Timestamp time.Time

	// CorrelationID links the measurement back to the original audit event
	CorrelationID string
}

// MeasurementToUtilization converts an auditdomain.Measurement to a UtilizationMeasurement
// for the MDS publishing path. The conversion maps:
//   - AccountID.String() -> TenantID
//   - Attributes["service"] -> ServiceName
//   - Attributes["operation"] -> OperationType
//   - Quantity + instrument lookup -> Amount
//   - Period.Start -> Timestamp
//   - ID.String() -> CorrelationID
func MeasurementToUtilization(m *auditdomain.Measurement) *UtilizationMeasurement {
	unitAttr := "operation"
	if attr, ok := m.Attributes["unit"]; ok {
		unitAttr = attr
	}
	instrument := InstrumentForMeasurementType(unitAttr)

	return &UtilizationMeasurement{
		TenantID:      m.AccountID.String(),
		ServiceName:   m.Attributes["service"],
		OperationType: m.Attributes["operation"],
		Amount:        quantity.NewAsset(m.Quantity, instrument),
		Timestamp:     m.Period.Start,
		CorrelationID: m.ID.String(),
	}
}
