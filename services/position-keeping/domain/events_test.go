package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransactionCaptured_EventType(t *testing.T) {
	event := &domain.TransactionCaptured{}
	assert.Equal(t, "position_keeping.transaction_captured.v1", event.EventType())
}

func TestTransactionCaptured_ToProto(t *testing.T) {
	logID := uuid.New()
	txID := uuid.New()
	timestamp := time.Now().UTC()

	money, err := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	require.NoError(t, err)

	event := &domain.TransactionCaptured{
		LogID:         logID,
		AccountID:     "ACC-123",
		TransactionID: txID,
		Amount:        money,
		Direction:     domain.PostingDirectionDebit,
		Source:        domain.TransactionSourceAutomated,
		Description:   "Test transaction",
		Reference:     "REF-001",
		CorrelationID: "CORR-123",
		Timestamp:     timestamp,
		Version:       1,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionCapturedEvent)
	require.True(t, ok, "should convert to TransactionCapturedEvent")

	assert.Equal(t, logID.String(), proto.LogId)
	assert.Equal(t, "ACC-123", proto.AccountId)
	assert.Equal(t, txID.String(), proto.TransactionId)
	assert.Equal(t, int64(10000), proto.AmountCents)
	assert.Equal(t, "GBP", proto.InstrumentCode)
	assert.Equal(t, "DEBIT", proto.Direction)
	assert.Equal(t, "AUTOMATED", proto.Source)
	assert.Equal(t, "Test transaction", proto.Description)
	assert.Equal(t, "REF-001", proto.Reference)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(1), proto.Version)
	assert.NotNil(t, proto.Timestamp)

	// Verify InstrumentAmount is populated for multi-asset support
	require.NotNil(t, proto.InstrumentAmount, "InstrumentAmount should be populated")
	assert.Equal(t, "100", proto.InstrumentAmount.Amount)
	assert.Equal(t, "GBP", proto.InstrumentAmount.InstrumentCode)
	assert.Equal(t, int32(1), proto.InstrumentAmount.Version)
}

func TestTransactionCaptured_ToProto_JPY(t *testing.T) {
	logID := uuid.New()
	txID := uuid.New()
	timestamp := time.Now().UTC()

	// JPY has 0 decimal places, so 1000 JPY = 1000 minor units (not 100000)
	money, err := domain.NewMoney(decimal.NewFromInt(1000), domain.CurrencyJPY)
	require.NoError(t, err)

	event := &domain.TransactionCaptured{
		LogID:         logID,
		AccountID:     "ACC-456",
		TransactionID: txID,
		Amount:        money,
		Direction:     domain.PostingDirectionCredit,
		Source:        domain.TransactionSourceManual,
		Description:   "JPY transaction",
		Reference:     "REF-JPY",
		CorrelationID: "CORR-JPY",
		Timestamp:     timestamp,
		Version:       1,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionCapturedEvent)
	require.True(t, ok)

	// Critical assertion: JPY amount should be 1000, not 100000
	// JPY has 0 decimal places, so no multiplication by 100
	assert.Equal(t, int64(1000), proto.AmountCents, "JPY should not be multiplied by 100")
	assert.Equal(t, "JPY", proto.InstrumentCode)

	// Verify InstrumentAmount is populated for JPY
	require.NotNil(t, proto.InstrumentAmount, "InstrumentAmount should be populated")
	assert.Equal(t, "1000", proto.InstrumentAmount.Amount)
	assert.Equal(t, "JPY", proto.InstrumentAmount.InstrumentCode)
	assert.Equal(t, int32(1), proto.InstrumentAmount.Version)
}

func TestTransactionAmended_EventType(t *testing.T) {
	event := &domain.TransactionAmended{}
	assert.Equal(t, "position_keeping.transaction_amended.v1", event.EventType())
}

func TestTransactionAmended_ToProto(t *testing.T) {
	logID := uuid.New()
	timestamp := time.Now().UTC()

	event := &domain.TransactionAmended{
		LogID:         logID,
		AccountID:     "ACC-123",
		Reason:        "Correction required",
		AmendedBy:     "user@example.com",
		CorrelationID: "CORR-123",
		Timestamp:     timestamp,
		Version:       2,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionAmendedEvent)
	require.True(t, ok)

	assert.Equal(t, logID.String(), proto.LogId)
	assert.Equal(t, "ACC-123", proto.AccountId)
	assert.Equal(t, "Correction required", proto.Reason)
	assert.Equal(t, "user@example.com", proto.AmendedBy)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(2), proto.Version)
}

func TestTransactionReconciled_EventType(t *testing.T) {
	event := &domain.TransactionReconciled{}
	assert.Equal(t, "position_keeping.transaction_reconciled.v1", event.EventType())
}

func TestTransactionReconciled_ToProto(t *testing.T) {
	logID := uuid.New()
	timestamp := time.Now().UTC()

	event := &domain.TransactionReconciled{
		LogID:                logID,
		AccountID:            "ACC-123",
		ReconciliationStatus: domain.ReconciliationStatusMatched,
		Reason:               "Automatic reconciliation",
		ReconciledBy:         "system",
		CorrelationID:        "CORR-123",
		Timestamp:            timestamp,
		Version:              2,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionReconciledEvent)
	require.True(t, ok)

	assert.Equal(t, logID.String(), proto.LogId)
	assert.Equal(t, "ACC-123", proto.AccountId)
	assert.Equal(t, "auto_reconciled", proto.ReconciliationStatus)
	assert.Equal(t, "Automatic reconciliation", proto.Reason)
	assert.Equal(t, "system", proto.ReconciledBy)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(2), proto.Version)
}

func TestTransactionPosted_EventType(t *testing.T) {
	event := &domain.TransactionPosted{}
	assert.Equal(t, "position_keeping.transaction_posted.v1", event.EventType())
}

func TestTransactionPosted_ToProto(t *testing.T) {
	logID := uuid.New()
	timestamp := time.Now().UTC()

	event := &domain.TransactionPosted{
		LogID:            logID,
		AccountID:        "ACC-123",
		PostingReference: "POST-001",
		Reason:           "Posted to ledger",
		PostedBy:         "system",
		CorrelationID:    "CORR-123",
		Timestamp:        timestamp,
		Version:          3,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionPostedEvent)
	require.True(t, ok)

	assert.Equal(t, logID.String(), proto.LogId)
	assert.Equal(t, "ACC-123", proto.AccountId)
	assert.Equal(t, "POST-001", proto.PostingReference)
	assert.Equal(t, "Posted to ledger", proto.Reason)
	assert.Equal(t, "system", proto.PostedBy)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(3), proto.Version)
}

func TestTransactionRejected_EventType(t *testing.T) {
	event := &domain.TransactionRejected{}
	assert.Equal(t, "position_keeping.transaction_rejected.v1", event.EventType())
}

func TestTransactionRejected_ToProto(t *testing.T) {
	logID := uuid.New()
	timestamp := time.Now().UTC()

	event := &domain.TransactionRejected{
		LogID:         logID,
		AccountID:     "ACC-123",
		Reason:        "Invalid amount",
		RejectedBy:    "validator",
		CorrelationID: "CORR-123",
		Timestamp:     timestamp,
		Version:       1,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionRejectedEvent)
	require.True(t, ok)

	assert.Equal(t, logID.String(), proto.LogId)
	assert.Equal(t, "ACC-123", proto.AccountId)
	assert.Equal(t, "Invalid amount", proto.Reason)
	assert.Equal(t, "validator", proto.RejectedBy)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(1), proto.Version)
}

func TestTransactionFailed_EventType(t *testing.T) {
	event := &domain.TransactionFailed{}
	assert.Equal(t, "position_keeping.transaction_failed.v1", event.EventType())
}

func TestTransactionFailed_ToProto(t *testing.T) {
	logID := uuid.New()
	timestamp := time.Now().UTC()

	event := &domain.TransactionFailed{
		LogID:         logID,
		AccountID:     "ACC-123",
		FailureReason: "Database connection lost",
		ErrorCode:     commonv1.ErrorCode_ERROR_CODE_INTERNAL,
		CorrelationID: "CORR-123",
		Timestamp:     timestamp,
		Version:       1,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionFailedEvent)
	require.True(t, ok)

	assert.Equal(t, logID.String(), proto.LogId)
	assert.Equal(t, "ACC-123", proto.AccountId)
	assert.Equal(t, "Database connection lost", proto.FailureReason)
	assert.Equal(t, commonv1.ErrorCode_ERROR_CODE_INTERNAL, proto.ErrorCodeEnum)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(1), proto.Version)
}

func TestTransactionCancelled_EventType(t *testing.T) {
	event := &domain.TransactionCancelled{}
	assert.Equal(t, "position_keeping.transaction_cancelled.v1", event.EventType())
}

func TestTransactionCancelled_ToProto(t *testing.T) {
	logID := uuid.New()
	timestamp := time.Now().UTC()

	event := &domain.TransactionCancelled{
		LogID:         logID,
		AccountID:     "ACC-123",
		Reason:        "User requested cancellation",
		CancelledBy:   "user@example.com",
		CorrelationID: "CORR-123",
		Timestamp:     timestamp,
		Version:       1,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.TransactionCancelledEvent)
	require.True(t, ok)

	assert.Equal(t, logID.String(), proto.LogId)
	assert.Equal(t, "ACC-123", proto.AccountId)
	assert.Equal(t, "User requested cancellation", proto.Reason)
	assert.Equal(t, "user@example.com", proto.CancelledBy)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(1), proto.Version)
}

func TestBulkTransactionCaptured_EventType(t *testing.T) {
	event := &domain.BulkTransactionCaptured{}
	assert.Equal(t, "position_keeping.bulk_transaction_captured.v1", event.EventType())
}

func TestBulkTransactionCaptured_ToProto(t *testing.T) {
	batchID := uuid.New()
	logID1 := uuid.New()
	logID2 := uuid.New()
	timestamp := time.Now().UTC()

	event := &domain.BulkTransactionCaptured{
		BatchID:          batchID,
		TransactionCount: 2,
		LogIDs:           []uuid.UUID{logID1, logID2},
		Source:           domain.TransactionSourceImported,
		CorrelationID:    "CORR-123",
		Timestamp:        timestamp,
		Version:          1,
	}

	protoEvent := event.ToProto()
	require.NotNil(t, protoEvent)

	proto, ok := protoEvent.(*eventsv1.BulkTransactionCapturedEvent)
	require.True(t, ok)

	assert.Equal(t, batchID.String(), proto.BatchId)
	assert.Equal(t, int32(2), proto.TransactionCount)
	assert.Len(t, proto.LogIds, 2)
	assert.Equal(t, logID1.String(), proto.LogIds[0])
	assert.Equal(t, logID2.String(), proto.LogIds[1])
	assert.Equal(t, "IMPORTED", proto.Source)
	assert.Equal(t, "CORR-123", proto.CorrelationId)
	assert.Equal(t, int64(1), proto.Version)
}

func TestDomainEvent_AggregateID(t *testing.T) {
	logID := uuid.New()
	batchID := uuid.New()

	tests := []struct {
		name              string
		event             domain.DomainEvent
		expectedAggregate string
	}{
		{
			name: "TransactionCaptured",
			event: &domain.TransactionCaptured{
				LogID: logID,
			},
			expectedAggregate: logID.String(),
		},
		{
			name: "TransactionAmended",
			event: &domain.TransactionAmended{
				LogID: logID,
			},
			expectedAggregate: logID.String(),
		},
		{
			name: "TransactionReconciled",
			event: &domain.TransactionReconciled{
				LogID: logID,
			},
			expectedAggregate: logID.String(),
		},
		{
			name: "TransactionPosted",
			event: &domain.TransactionPosted{
				LogID: logID,
			},
			expectedAggregate: logID.String(),
		},
		{
			name: "TransactionRejected",
			event: &domain.TransactionRejected{
				LogID: logID,
			},
			expectedAggregate: logID.String(),
		},
		{
			name: "TransactionFailed",
			event: &domain.TransactionFailed{
				LogID: logID,
			},
			expectedAggregate: logID.String(),
		},
		{
			name: "TransactionCancelled",
			event: &domain.TransactionCancelled{
				LogID: logID,
			},
			expectedAggregate: logID.String(),
		},
		{
			name: "BulkTransactionCaptured",
			event: &domain.BulkTransactionCaptured{
				BatchID: batchID,
			},
			expectedAggregate: batchID.String(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedAggregate, tt.event.AggregateID())
		})
	}
}

func TestDomainEvent_OccurredAt(t *testing.T) {
	timestamp := time.Now().UTC()

	tests := []struct {
		name  string
		event domain.DomainEvent
	}{
		{
			name: "TransactionCaptured",
			event: &domain.TransactionCaptured{
				Timestamp: timestamp,
			},
		},
		{
			name: "TransactionAmended",
			event: &domain.TransactionAmended{
				Timestamp: timestamp,
			},
		},
		{
			name: "TransactionReconciled",
			event: &domain.TransactionReconciled{
				Timestamp: timestamp,
			},
		},
		{
			name: "TransactionPosted",
			event: &domain.TransactionPosted{
				Timestamp: timestamp,
			},
		},
		{
			name: "TransactionRejected",
			event: &domain.TransactionRejected{
				Timestamp: timestamp,
			},
		},
		{
			name: "TransactionFailed",
			event: &domain.TransactionFailed{
				Timestamp: timestamp,
			},
		},
		{
			name: "TransactionCancelled",
			event: &domain.TransactionCancelled{
				Timestamp: timestamp,
			},
		},
		{
			name: "BulkTransactionCaptured",
			event: &domain.BulkTransactionCaptured{
				Timestamp: timestamp,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, timestamp, tt.event.OccurredAt())
		})
	}
}

// TestTransactionCaptured_AllCurrencies tests instrument code conversion for all supported currencies
func TestTransactionCaptured_AllCurrencies(t *testing.T) {
	tests := []struct {
		name                   string
		currency               domain.Currency
		expectedInstrumentCode string
	}{
		{"USD", domain.CurrencyUSD, "USD"},
		{"EUR", domain.CurrencyEUR, "EUR"},
		{"CHF", domain.CurrencyCHF, "CHF"},
		{"CAD", domain.CurrencyCAD, "CAD"},
		{"AUD", domain.CurrencyAUD, "AUD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := domain.NewMoney(decimal.NewFromInt(100), tt.currency)
			require.NoError(t, err)

			event := &domain.TransactionCaptured{
				LogID:         uuid.New(),
				AccountID:     "ACC-123",
				TransactionID: uuid.New(),
				Amount:        money,
				Direction:     domain.PostingDirectionDebit,
				Source:        domain.TransactionSourceManual,
				Timestamp:     time.Now().UTC(),
				Version:       1,
			}

			protoEvent := event.ToProto()
			proto, ok := protoEvent.(*eventsv1.TransactionCapturedEvent)
			require.True(t, ok)
			assert.Equal(t, tt.expectedInstrumentCode, proto.InstrumentCode)
		})
	}
}

// TestTransactionReconciled_AllStatuses tests reconciliation status conversion for all statuses
func TestTransactionReconciled_AllStatuses(t *testing.T) {
	tests := []struct {
		name                string
		status              domain.ReconciliationStatus
		expectedProtoStatus string
	}{
		{"matched", domain.ReconciliationStatusMatched, "auto_reconciled"},
		{"resolved", domain.ReconciliationStatusResolved, "manually_reconciled"},
		{"mismatched", domain.ReconciliationStatusMismatched, "reconciled_with_discrepancy"},
		{"unreconciled", domain.ReconciliationStatusUnreconciled, "unreconciled"},
		{"unknown defaults to unreconciled", domain.ReconciliationStatus("UNKNOWN"), "unreconciled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := &domain.TransactionReconciled{
				LogID:                uuid.New(),
				AccountID:            "ACC-123",
				ReconciliationStatus: tt.status,
				Reason:               "Test",
				ReconciledBy:         "system",
				CorrelationID:        "CORR-123",
				Timestamp:            time.Now().UTC(),
				Version:              1,
			}

			protoEvent := event.ToProto()
			proto, ok := protoEvent.(*eventsv1.TransactionReconciledEvent)
			require.True(t, ok)
			assert.Equal(t, tt.expectedProtoStatus, proto.ReconciliationStatus)
		})
	}
}
