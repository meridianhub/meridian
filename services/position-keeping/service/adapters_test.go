package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	messagingpkg "github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	eventspkg "github.com/meridianhub/meridian/shared/platform/events"
)

// TestToProtoMoneyAmount tests conversion of domain.Money to protobuf MoneyAmount
func TestToProtoMoneyAmount(t *testing.T) {
	tests := []struct {
		name          string
		money         domain.Money
		expectedUnits int64
		expectedNanos int32
		expectedCode  string
	}{
		{
			name:          "GBP with 2 decimal places",
			money:         mustNewMoney("123.45", domain.CurrencyGBP),
			expectedUnits: 123,
			expectedNanos: 450000000,
			expectedCode:  "GBP",
		},
		{
			name:          "GBP with fractional cents",
			money:         mustNewMoney("123.456789", domain.CurrencyGBP),
			expectedUnits: 123,
			expectedNanos: 456789000,
			expectedCode:  "GBP",
		},
		{
			name:          "JPY with 0 decimal places",
			money:         mustNewMoney("12345", domain.CurrencyJPY),
			expectedUnits: 12345,
			expectedNanos: 0,
			expectedCode:  "JPY",
		},
		{
			name:          "USD with cents",
			money:         mustNewMoney("100.99", domain.CurrencyUSD),
			expectedUnits: 100,
			expectedNanos: 990000000,
			expectedCode:  "USD",
		},
		{
			name:          "EUR zero amount",
			money:         mustNewMoney("0.00", domain.CurrencyEUR),
			expectedUnits: 0,
			expectedNanos: 0,
			expectedCode:  "EUR",
		},
		{
			name:          "negative amount",
			money:         mustNewMoney("-50.25", domain.CurrencyGBP),
			expectedUnits: -50,
			expectedNanos: -250000000,
			expectedCode:  "GBP",
		},
		{
			name:          "large amount",
			money:         mustNewMoney("999999999.99", domain.CurrencyUSD),
			expectedUnits: 999999999,
			expectedNanos: 990000000,
			expectedCode:  "USD",
		},
		{
			name:          "very small fractional amount",
			money:         mustNewMoney("0.001", domain.CurrencyGBP),
			expectedUnits: 0,
			expectedNanos: 1000000,
			expectedCode:  "GBP",
		},
		{
			name:          "CHF currency",
			money:         mustNewMoney("75.50", domain.CurrencyCHF),
			expectedUnits: 75,
			expectedNanos: 500000000,
			expectedCode:  "CHF",
		},
		{
			name:          "CAD currency",
			money:         mustNewMoney("200.15", domain.CurrencyCAD),
			expectedUnits: 200,
			expectedNanos: 150000000,
			expectedCode:  "CAD",
		},
		{
			name:          "AUD currency",
			money:         mustNewMoney("150.33", domain.CurrencyAUD),
			expectedUnits: 150,
			expectedNanos: 330000000,
			expectedCode:  "AUD",
		},
		{
			name:          "nanos clamped to max (positive overflow)",
			money:         mustNewMoney("1.999999999999", domain.CurrencyGBP),
			expectedUnits: 1,
			expectedNanos: 999999999, // Clamped to int32 max nanos
			expectedCode:  "GBP",
		},
		{
			name:          "nanos clamped to min (negative overflow)",
			money:         mustNewMoney("-1.999999999999", domain.CurrencyGBP),
			expectedUnits: -1,
			expectedNanos: -999999999, // Clamped to int32 min nanos
			expectedCode:  "GBP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use reflection to call unexported function via exported method
			proto := callToProtoMoneyAmount(tt.money)

			require.NotNil(t, proto)
			require.NotNil(t, proto.Amount)
			assert.Equal(t, tt.expectedCode, proto.Amount.CurrencyCode)
			assert.Equal(t, tt.expectedUnits, proto.Amount.Units)
			assert.Equal(t, tt.expectedNanos, proto.Amount.Nanos)
		})
	}
}

// TestToProtoPostingDirection tests conversion of domain.PostingDirection to protobuf
func TestToProtoPostingDirection(t *testing.T) {
	tests := []struct {
		name     string
		domain   domain.PostingDirection
		expected commonv1.PostingDirection
	}{
		{
			name:     "debit direction",
			domain:   domain.PostingDirectionDebit,
			expected: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		},
		{
			name:     "credit direction",
			domain:   domain.PostingDirectionCredit,
			expected: commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
		},
		{
			name:     "invalid direction returns unspecified",
			domain:   domain.PostingDirection("INVALID"),
			expected: commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED,
		},
		{
			name:     "empty direction returns unspecified",
			domain:   domain.PostingDirection(""),
			expected: commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callToProtoPostingDirection(tt.domain)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestToProtoTransactionStatus tests conversion of domain.TransactionStatus to protobuf
func TestToProtoTransactionStatus(t *testing.T) {
	tests := []struct {
		name     string
		domain   domain.TransactionStatus
		expected commonv1.TransactionStatus
	}{
		{
			name:     "pending status",
			domain:   domain.TransactionStatusPending,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		},
		{
			name:     "posted status",
			domain:   domain.TransactionStatusPosted,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		},
		{
			name:     "failed status",
			domain:   domain.TransactionStatusFailed,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
		},
		{
			name:     "cancelled status",
			domain:   domain.TransactionStatusCancelled,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
		},
		{
			name:     "reversed status",
			domain:   domain.TransactionStatusReversed,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED,
		},
		{
			name:     "reconciled maps to posted",
			domain:   domain.TransactionStatusReconciled,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
		},
		{
			name:     "rejected maps to failed",
			domain:   domain.TransactionStatusRejected,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
		},
		{
			name:     "amended maps to pending",
			domain:   domain.TransactionStatusAmended,
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
		},
		{
			name:     "invalid status returns unspecified",
			domain:   domain.TransactionStatus("INVALID"),
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
		},
		{
			name:     "empty status returns unspecified",
			domain:   domain.TransactionStatus(""),
			expected: commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := callToProtoTransactionStatus(tt.domain)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestToProtoTransactionLogEntry tests conversion of domain.TransactionLogEntry to protobuf
func TestToProtoTransactionLogEntry(t *testing.T) {
	entryID := uuid.New()
	transactionID := uuid.New()
	timestamp := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		entry    *domain.TransactionLogEntry
		validate func(t *testing.T, proto *positionkeepingv1.TransactionLogEntry)
	}{
		{
			name: "complete entry with all fields",
			entry: &domain.TransactionLogEntry{
				EntryID:       entryID,
				TransactionID: transactionID,
				AccountID:     "ACC-123",
				Amount:        mustNewMoney("100.50", domain.CurrencyGBP),
				Direction:     domain.PostingDirectionDebit,
				Timestamp:     timestamp,
				Description:   "Test transaction",
				Reference:     "REF-001",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLogEntry) {
				require.NotNil(t, proto)
				assert.Equal(t, entryID.String(), proto.EntryId)
				assert.Equal(t, transactionID.String(), proto.TransactionId)
				assert.Equal(t, "ACC-123", proto.AccountId)
				require.NotNil(t, proto.Amount)
				assert.Equal(t, "GBP", proto.Amount.Amount.CurrencyCode)
				assert.Equal(t, int64(100), proto.Amount.Amount.Units)
				assert.Equal(t, int32(500000000), proto.Amount.Amount.Nanos)
				assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, proto.Direction)
				assert.NotNil(t, proto.Timestamp)
				assert.Equal(t, timestamp.Unix(), proto.Timestamp.Seconds)
				assert.Equal(t, "Test transaction", proto.Description)
				assert.Equal(t, "REF-001", proto.Reference)
			},
		},
		{
			name: "credit entry",
			entry: &domain.TransactionLogEntry{
				EntryID:       entryID,
				TransactionID: transactionID,
				AccountID:     "ACC-456",
				Amount:        mustNewMoney("250.00", domain.CurrencyUSD),
				Direction:     domain.PostingDirectionCredit,
				Timestamp:     timestamp,
				Description:   "Credit entry",
				Reference:     "REF-002",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLogEntry) {
				require.NotNil(t, proto)
				assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_CREDIT, proto.Direction)
				assert.Equal(t, "USD", proto.Amount.Amount.CurrencyCode)
			},
		},
		{
			name: "JPY entry with no decimals",
			entry: &domain.TransactionLogEntry{
				EntryID:       entryID,
				TransactionID: transactionID,
				AccountID:     "ACC-789",
				Amount:        mustNewMoney("50000", domain.CurrencyJPY),
				Direction:     domain.PostingDirectionDebit,
				Timestamp:     timestamp,
				Description:   "JPY transaction",
				Reference:     "REF-003",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLogEntry) {
				require.NotNil(t, proto)
				assert.Equal(t, "JPY", proto.Amount.Amount.CurrencyCode)
				assert.Equal(t, int64(50000), proto.Amount.Amount.Units)
				assert.Equal(t, int32(0), proto.Amount.Amount.Nanos)
			},
		},
		{
			name: "empty description and reference",
			entry: &domain.TransactionLogEntry{
				EntryID:       entryID,
				TransactionID: transactionID,
				AccountID:     "ACC-999",
				Amount:        mustNewMoney("10.00", domain.CurrencyEUR),
				Direction:     domain.PostingDirectionDebit,
				Timestamp:     timestamp,
				Description:   "",
				Reference:     "",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLogEntry) {
				require.NotNil(t, proto)
				assert.Empty(t, proto.Description)
				assert.Empty(t, proto.Reference)
			},
		},
		{
			name:  "nil entry returns nil",
			entry: nil,
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLogEntry) {
				assert.Nil(t, proto)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := callToProtoTransactionLogEntry(tt.entry)
			tt.validate(t, proto)
		})
	}
}

// TestToProtoTransactionLineage tests conversion of domain.TransactionLineage to protobuf
func TestToProtoTransactionLineage(t *testing.T) {
	transactionID := uuid.New()
	parentID := uuid.New()
	child1ID := uuid.New()
	child2ID := uuid.New()
	related1ID := uuid.New()
	related2ID := uuid.New()

	tests := []struct {
		name     string
		lineage  *domain.TransactionLineage
		validate func(t *testing.T, proto *positionkeepingv1.TransactionLineage)
	}{
		{
			name: "complete lineage with parent, children, and related",
			lineage: mustNewTransactionLineage(
				transactionID,
				"PAYMENT",
				&parentID,
				[]uuid.UUID{child1ID, child2ID},
				[]uuid.UUID{related1ID, related2ID},
			),
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLineage) {
				require.NotNil(t, proto)
				assert.Equal(t, transactionID.String(), proto.TransactionId)
				assert.Equal(t, "PAYMENT", proto.TransactionType)
				assert.Equal(t, parentID.String(), proto.ParentTransactionId)
				require.Len(t, proto.ChildTransactionIds, 2)
				assert.Contains(t, proto.ChildTransactionIds, child1ID.String())
				assert.Contains(t, proto.ChildTransactionIds, child2ID.String())
				require.Len(t, proto.RelatedTransactionIds, 2)
				assert.Contains(t, proto.RelatedTransactionIds, related1ID.String())
				assert.Contains(t, proto.RelatedTransactionIds, related2ID.String())
				assert.NotNil(t, proto.CreatedAt)
			},
		},
		{
			name: "lineage with no parent",
			lineage: mustNewTransactionLineage(
				transactionID,
				"TRANSFER",
				nil,
				[]uuid.UUID{child1ID},
				nil,
			),
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLineage) {
				require.NotNil(t, proto)
				assert.Equal(t, transactionID.String(), proto.TransactionId)
				assert.Equal(t, "TRANSFER", proto.TransactionType)
				assert.Empty(t, proto.ParentTransactionId)
				require.Len(t, proto.ChildTransactionIds, 1)
				assert.Equal(t, child1ID.String(), proto.ChildTransactionIds[0])
				assert.Empty(t, proto.RelatedTransactionIds)
			},
		},
		{
			name: "lineage with no children or related",
			lineage: mustNewTransactionLineage(
				transactionID,
				"DEPOSIT",
				&parentID,
				nil,
				nil,
			),
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLineage) {
				require.NotNil(t, proto)
				assert.Equal(t, transactionID.String(), proto.TransactionId)
				assert.Equal(t, "DEPOSIT", proto.TransactionType)
				assert.Equal(t, parentID.String(), proto.ParentTransactionId)
				assert.Empty(t, proto.ChildTransactionIds)
				assert.Empty(t, proto.RelatedTransactionIds)
			},
		},
		{
			name: "lineage with only transaction ID",
			lineage: mustNewTransactionLineage(
				transactionID,
				"WITHDRAWAL",
				nil,
				nil,
				nil,
			),
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLineage) {
				require.NotNil(t, proto)
				assert.Equal(t, transactionID.String(), proto.TransactionId)
				assert.Equal(t, "WITHDRAWAL", proto.TransactionType)
				assert.Empty(t, proto.ParentTransactionId)
				assert.Empty(t, proto.ChildTransactionIds)
				assert.Empty(t, proto.RelatedTransactionIds)
			},
		},
		{
			name:    "nil lineage returns nil",
			lineage: nil,
			validate: func(t *testing.T, proto *positionkeepingv1.TransactionLineage) {
				assert.Nil(t, proto)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := callToProtoTransactionLineage(tt.lineage)
			tt.validate(t, proto)
		})
	}
}

// TestToProtoAuditTrailEntry tests conversion of domain.AuditTrailEntry to protobuf
func TestToProtoAuditTrailEntry(t *testing.T) {
	auditID := uuid.New()
	timestamp := time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name     string
		entry    *domain.AuditTrailEntry
		validate func(t *testing.T, proto *positionkeepingv1.AuditTrailEntry)
	}{
		{
			name: "complete audit entry with all fields",
			entry: &domain.AuditTrailEntry{
				AuditID:   auditID,
				Timestamp: timestamp,
				UserID:    "user-123",
				Action:    "INITIATED",
				Details:   "Transaction initiated by user",
				IPAddress: "192.168.1.100",
				SystemContext: map[string]string{
					"service":   "position-keeping",
					"version":   "1.0.0",
					"requestId": "req-789",
				},
			},
			validate: func(t *testing.T, proto *positionkeepingv1.AuditTrailEntry) {
				require.NotNil(t, proto)
				assert.Equal(t, auditID.String(), proto.AuditId)
				assert.NotNil(t, proto.Timestamp)
				assert.Equal(t, timestamp.Unix(), proto.Timestamp.Seconds)
				assert.Equal(t, "user-123", proto.UserId)
				assert.Equal(t, "INITIATED", proto.Action)
				assert.Equal(t, "Transaction initiated by user", proto.Details)
				assert.Equal(t, "192.168.1.100", proto.IpAddress)
				require.NotNil(t, proto.SystemContext)
				assert.Equal(t, "position-keeping", proto.SystemContext["service"])
				assert.Equal(t, "1.0.0", proto.SystemContext["version"])
				assert.Equal(t, "req-789", proto.SystemContext["requestId"])
			},
		},
		{
			name: "audit entry with empty optional fields",
			entry: &domain.AuditTrailEntry{
				AuditID:       auditID,
				Timestamp:     timestamp,
				UserID:        "user-456",
				Action:        "POSTED",
				Details:       "",
				IPAddress:     "",
				SystemContext: nil,
			},
			validate: func(t *testing.T, proto *positionkeepingv1.AuditTrailEntry) {
				require.NotNil(t, proto)
				assert.Equal(t, auditID.String(), proto.AuditId)
				assert.Equal(t, "user-456", proto.UserId)
				assert.Equal(t, "POSTED", proto.Action)
				assert.Empty(t, proto.Details)
				assert.Empty(t, proto.IpAddress)
				require.NotNil(t, proto.SystemContext)
				assert.Empty(t, proto.SystemContext)
			},
		},
		{
			name: "audit entry with empty system context map",
			entry: &domain.AuditTrailEntry{
				AuditID:       auditID,
				Timestamp:     timestamp,
				UserID:        "user-789",
				Action:        "RECONCILED",
				Details:       "Auto-reconciled",
				IPAddress:     "10.0.0.1",
				SystemContext: map[string]string{},
			},
			validate: func(t *testing.T, proto *positionkeepingv1.AuditTrailEntry) {
				require.NotNil(t, proto)
				require.NotNil(t, proto.SystemContext)
				assert.Empty(t, proto.SystemContext)
			},
		},
		{
			name:  "nil audit entry returns nil",
			entry: nil,
			validate: func(t *testing.T, proto *positionkeepingv1.AuditTrailEntry) {
				assert.Nil(t, proto)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := callToProtoAuditTrailEntry(tt.entry)
			tt.validate(t, proto)
		})
	}
}

// TestToProtoStatusTracking tests conversion of domain.StatusTracking to protobuf
func TestToProtoStatusTracking(t *testing.T) {
	now := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	previousStatus := domain.TransactionStatusPending

	tests := []struct {
		name     string
		tracking *domain.StatusTracking
		validate func(t *testing.T, proto *positionkeepingv1.StatusTracking)
	}{
		{
			name: "status tracking with previous status",
			tracking: &domain.StatusTracking{
				CurrentStatus:   domain.TransactionStatusPosted,
				PreviousStatus:  &previousStatus,
				StatusUpdatedAt: now,
				StatusReason:    "Successfully posted",
				FailureReason:   "",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.StatusTracking) {
				require.NotNil(t, proto)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, proto.CurrentStatus)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, proto.PreviousStatus)
				assert.NotNil(t, proto.StatusUpdatedAt)
				assert.Equal(t, now.Unix(), proto.StatusUpdatedAt.Seconds)
				assert.Equal(t, "Successfully posted", proto.StatusReason)
				assert.Empty(t, proto.FailureReason)
			},
		},
		{
			name: "status tracking without previous status",
			tracking: &domain.StatusTracking{
				CurrentStatus:   domain.TransactionStatusPending,
				PreviousStatus:  nil,
				StatusUpdatedAt: now,
				StatusReason:    "Initial creation",
				FailureReason:   "",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.StatusTracking) {
				require.NotNil(t, proto)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, proto.CurrentStatus)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED, proto.PreviousStatus)
				assert.Equal(t, "Initial creation", proto.StatusReason)
				assert.Empty(t, proto.FailureReason)
			},
		},
		{
			name: "failed status with failure reason",
			tracking: &domain.StatusTracking{
				CurrentStatus:   domain.TransactionStatusFailed,
				PreviousStatus:  &previousStatus,
				StatusUpdatedAt: now,
				StatusReason:    "Transaction failed",
				FailureReason:   "Insufficient funds",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.StatusTracking) {
				require.NotNil(t, proto)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, proto.CurrentStatus)
				assert.Equal(t, "Transaction failed", proto.StatusReason)
				assert.Equal(t, "Insufficient funds", proto.FailureReason)
			},
		},
		{
			name: "cancelled status",
			tracking: &domain.StatusTracking{
				CurrentStatus:   domain.TransactionStatusCancelled,
				PreviousStatus:  &previousStatus,
				StatusUpdatedAt: now,
				StatusReason:    "User cancelled",
				FailureReason:   "",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.StatusTracking) {
				require.NotNil(t, proto)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, proto.CurrentStatus)
			},
		},
		{
			name: "reversed status",
			tracking: &domain.StatusTracking{
				CurrentStatus:   domain.TransactionStatusReversed,
				PreviousStatus:  &previousStatus,
				StatusUpdatedAt: now,
				StatusReason:    "Reversed by admin",
				FailureReason:   "",
			},
			validate: func(t *testing.T, proto *positionkeepingv1.StatusTracking) {
				require.NotNil(t, proto)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED, proto.CurrentStatus)
			},
		},
		{
			name:     "nil status tracking returns nil",
			tracking: nil,
			validate: func(t *testing.T, proto *positionkeepingv1.StatusTracking) {
				assert.Nil(t, proto)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := callToProtoStatusTracking(tt.tracking)
			tt.validate(t, proto)
		})
	}
}

// TestToProtoFinancialPositionLog tests conversion of domain.FinancialPositionLog to protobuf
func TestToProtoFinancialPositionLog(t *testing.T) {
	logID := uuid.New()
	transactionID := uuid.New()
	entryID := uuid.New()
	auditID := uuid.New()
	now := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		log      *domain.FinancialPositionLog
		validate func(t *testing.T, proto *positionkeepingv1.FinancialPositionLog)
	}{
		{
			name: "complete log with entries and audit trail",
			log: &domain.FinancialPositionLog{
				LogID:     logID,
				AccountID: "ACC-123",
				TransactionLogEntries: []*domain.TransactionLogEntry{
					{
						EntryID:       entryID,
						TransactionID: transactionID,
						AccountID:     "ACC-123",
						Amount:        mustNewMoney("100.50", domain.CurrencyGBP),
						Direction:     domain.PostingDirectionDebit,
						Timestamp:     now,
						Description:   "Test entry",
						Reference:     "REF-001",
					},
				},
				TransactionLineage: mustNewTransactionLineage(transactionID, "PAYMENT", nil, nil, nil),
				AuditTrail: []*domain.AuditTrailEntry{
					{
						AuditID:   auditID,
						Timestamp: now,
						UserID:    "user-123",
						Action:    "INITIATED",
						Details:   "Transaction initiated",
						IPAddress: "192.168.1.1",
						SystemContext: map[string]string{
							"service": "position-keeping",
						},
					},
				},
				StatusTracking: &domain.StatusTracking{
					CurrentStatus:   domain.TransactionStatusPending,
					PreviousStatus:  nil,
					StatusUpdatedAt: now,
					StatusReason:    "Initial creation",
					FailureReason:   "",
				},
				CreatedAt: now,
				UpdatedAt: now,
				Version:   1,
			},
			validate: func(t *testing.T, proto *positionkeepingv1.FinancialPositionLog) {
				require.NotNil(t, proto)
				assert.Equal(t, logID.String(), proto.LogId)
				assert.Equal(t, "ACC-123", proto.AccountId)
				require.Len(t, proto.TransactionLogEntries, 1)
				assert.Equal(t, entryID.String(), proto.TransactionLogEntries[0].EntryId)
				require.NotNil(t, proto.TransactionLineage)
				assert.Equal(t, transactionID.String(), proto.TransactionLineage.TransactionId)
				require.Len(t, proto.AuditTrail, 1)
				assert.Equal(t, auditID.String(), proto.AuditTrail[0].AuditId)
				require.NotNil(t, proto.StatusTracking)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, proto.StatusTracking.CurrentStatus)
				assert.NotNil(t, proto.CreatedAt)
				assert.NotNil(t, proto.UpdatedAt)
				assert.Equal(t, int64(1), proto.Version)
			},
		},
		{
			name: "log with empty entries and audit trail",
			log: &domain.FinancialPositionLog{
				LogID:                 logID,
				AccountID:             "ACC-456",
				TransactionLogEntries: []*domain.TransactionLogEntry{},
				TransactionLineage:    nil,
				AuditTrail:            []*domain.AuditTrailEntry{},
				StatusTracking: &domain.StatusTracking{
					CurrentStatus:   domain.TransactionStatusPending,
					PreviousStatus:  nil,
					StatusUpdatedAt: now,
					StatusReason:    "Initial creation",
					FailureReason:   "",
				},
				CreatedAt: now,
				UpdatedAt: now,
				Version:   1,
			},
			validate: func(t *testing.T, proto *positionkeepingv1.FinancialPositionLog) {
				require.NotNil(t, proto)
				assert.Equal(t, logID.String(), proto.LogId)
				assert.Equal(t, "ACC-456", proto.AccountId)
				assert.Empty(t, proto.TransactionLogEntries)
				assert.Nil(t, proto.TransactionLineage)
				assert.Empty(t, proto.AuditTrail)
				require.NotNil(t, proto.StatusTracking)
			},
		},
		{
			name: "log with multiple entries",
			log: &domain.FinancialPositionLog{
				LogID:     logID,
				AccountID: "ACC-789",
				TransactionLogEntries: []*domain.TransactionLogEntry{
					{
						EntryID:       uuid.New(),
						TransactionID: transactionID,
						AccountID:     "ACC-789",
						Amount:        mustNewMoney("50.00", domain.CurrencyUSD),
						Direction:     domain.PostingDirectionDebit,
						Timestamp:     now,
						Description:   "Entry 1",
						Reference:     "REF-001",
					},
					{
						EntryID:       uuid.New(),
						TransactionID: transactionID,
						AccountID:     "ACC-789",
						Amount:        mustNewMoney("25.00", domain.CurrencyUSD),
						Direction:     domain.PostingDirectionCredit,
						Timestamp:     now,
						Description:   "Entry 2",
						Reference:     "REF-002",
					},
				},
				TransactionLineage: nil,
				AuditTrail:         []*domain.AuditTrailEntry{},
				StatusTracking: &domain.StatusTracking{
					CurrentStatus:   domain.TransactionStatusPosted,
					PreviousStatus:  nil,
					StatusUpdatedAt: now,
					StatusReason:    "Posted successfully",
					FailureReason:   "",
				},
				CreatedAt: now,
				UpdatedAt: now,
				Version:   2,
			},
			validate: func(t *testing.T, proto *positionkeepingv1.FinancialPositionLog) {
				require.NotNil(t, proto)
				require.Len(t, proto.TransactionLogEntries, 2)
				assert.Equal(t, "Entry 1", proto.TransactionLogEntries[0].Description)
				assert.Equal(t, "Entry 2", proto.TransactionLogEntries[1].Description)
				assert.Equal(t, int64(2), proto.Version)
			},
		},
		{
			name: "log with JPY currency",
			log: &domain.FinancialPositionLog{
				LogID:     logID,
				AccountID: "ACC-JPY",
				TransactionLogEntries: []*domain.TransactionLogEntry{
					{
						EntryID:       entryID,
						TransactionID: transactionID,
						AccountID:     "ACC-JPY",
						Amount:        mustNewMoney("10000", domain.CurrencyJPY),
						Direction:     domain.PostingDirectionDebit,
						Timestamp:     now,
						Description:   "JPY transaction",
						Reference:     "REF-JPY",
					},
				},
				TransactionLineage: nil,
				AuditTrail:         []*domain.AuditTrailEntry{},
				StatusTracking:     domain.NewStatusTracking(),
				CreatedAt:          now,
				UpdatedAt:          now,
				Version:            1,
			},
			validate: func(t *testing.T, proto *positionkeepingv1.FinancialPositionLog) {
				require.NotNil(t, proto)
				require.Len(t, proto.TransactionLogEntries, 1)
				assert.Equal(t, "JPY", proto.TransactionLogEntries[0].Amount.Amount.CurrencyCode)
				assert.Equal(t, int64(10000), proto.TransactionLogEntries[0].Amount.Amount.Units)
				assert.Equal(t, int32(0), proto.TransactionLogEntries[0].Amount.Amount.Nanos)
			},
		},
		{
			name: "nil log returns nil",
			log:  nil,
			validate: func(t *testing.T, proto *positionkeepingv1.FinancialPositionLog) {
				assert.Nil(t, proto)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := callToProtoFinancialPositionLog(tt.log)
			tt.validate(t, proto)
		})
	}
}

// Helper functions to call unexported adapter functions via exported service methods

// callToProtoMoneyAmount calls the unexported toProtoMoneyAmount via a TransactionLogEntry conversion
func callToProtoMoneyAmount(money domain.Money) *commonv1.MoneyAmount {
	entry := &domain.TransactionLogEntry{
		EntryID:       uuid.New(),
		TransactionID: uuid.New(),
		AccountID:     "TEST",
		Amount:        money,
		Direction:     domain.PostingDirectionDebit,
		Timestamp:     time.Now(),
	}
	proto := callToProtoTransactionLogEntry(entry)
	if proto == nil {
		return nil
	}
	return proto.Amount
}

// callToProtoPostingDirection calls via service by creating a test service instance
func callToProtoPostingDirection(direction domain.PostingDirection) commonv1.PostingDirection {
	entry := &domain.TransactionLogEntry{
		EntryID:       uuid.New(),
		TransactionID: uuid.New(),
		AccountID:     "TEST",
		Amount:        mustNewMoney("100", domain.CurrencyGBP),
		Direction:     direction,
		Timestamp:     time.Now(),
	}
	proto := callToProtoTransactionLogEntry(entry)
	if proto == nil {
		return commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED
	}
	return proto.Direction
}

// callToProtoTransactionStatus calls via service by creating StatusTracking
func callToProtoTransactionStatus(status domain.TransactionStatus) commonv1.TransactionStatus {
	tracking := &domain.StatusTracking{
		CurrentStatus:   status,
		StatusUpdatedAt: time.Now(),
		StatusReason:    "test",
	}
	proto := callToProtoStatusTracking(tracking)
	if proto == nil {
		return commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
	}
	return proto.CurrentStatus
}

// callToProtoTransactionLogEntry uses reflection to access the unexported function
func callToProtoTransactionLogEntry(entry *domain.TransactionLogEntry) *positionkeepingv1.TransactionLogEntry {
	// Create a minimal FinancialPositionLog to trigger conversion
	if entry == nil {
		return nil
	}
	log := &domain.FinancialPositionLog{
		LogID:                 uuid.New(),
		AccountID:             "TEST",
		TransactionLogEntries: []*domain.TransactionLogEntry{entry},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	proto := callToProtoFinancialPositionLog(log)
	if proto == nil || len(proto.TransactionLogEntries) == 0 {
		return nil
	}
	return proto.TransactionLogEntries[0]
}

// callToProtoTransactionLineage calls via FinancialPositionLog conversion
func callToProtoTransactionLineage(lineage *domain.TransactionLineage) *positionkeepingv1.TransactionLineage {
	if lineage == nil {
		return nil
	}
	log := &domain.FinancialPositionLog{
		LogID:                 uuid.New(),
		AccountID:             "TEST",
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		TransactionLineage:    lineage,
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	proto := callToProtoFinancialPositionLog(log)
	if proto == nil {
		return nil
	}
	return proto.TransactionLineage
}

// callToProtoAuditTrailEntry calls via FinancialPositionLog conversion
func callToProtoAuditTrailEntry(entry *domain.AuditTrailEntry) *positionkeepingv1.AuditTrailEntry {
	if entry == nil {
		return nil
	}
	log := &domain.FinancialPositionLog{
		LogID:                 uuid.New(),
		AccountID:             "TEST",
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{entry},
		StatusTracking:        domain.NewStatusTracking(),
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	proto := callToProtoFinancialPositionLog(log)
	if proto == nil || len(proto.AuditTrail) == 0 {
		return nil
	}
	return proto.AuditTrail[0]
}

// callToProtoStatusTracking calls via FinancialPositionLog conversion
func callToProtoStatusTracking(tracking *domain.StatusTracking) *positionkeepingv1.StatusTracking {
	if tracking == nil {
		return nil
	}
	log := &domain.FinancialPositionLog{
		LogID:                 uuid.New(),
		AccountID:             "TEST",
		TransactionLogEntries: []*domain.TransactionLogEntry{},
		AuditTrail:            []*domain.AuditTrailEntry{},
		StatusTracking:        tracking,
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		Version:               1,
	}
	proto := callToProtoFinancialPositionLog(log)
	if proto == nil {
		return nil
	}
	return proto.StatusTracking
}

// callToProtoFinancialPositionLog creates a service instance and calls RetrieveFinancialPositionLog
func callToProtoFinancialPositionLog(log *domain.FinancialPositionLog) *positionkeepingv1.FinancialPositionLog {
	if log == nil {
		return nil
	}

	// Use the mock repository to return our test log
	mockRepo := new(MockRepository)
	mockRepo.On("FindByID", mock.Anything, log.LogID).Return(log, nil)

	mockEventPublisher := domain.NewInMemoryEventPublisher()
	mockIdempotency := new(MockIdempotencyService)
	mockMeasurementRepo := new(MockMeasurementRepository)

	outboxPub, err := messagingpkg.NewOutboxEventPublisher(eventspkg.NewPgxOutboxRepository(nil))
	if err != nil {
		return nil
	}

	svc, err := service.NewPositionKeepingService(mockRepo, mockMeasurementRepo, mockEventPublisher, mockIdempotency, outboxPub)
	if err != nil {
		return nil
	}

	// Call RetrieveFinancialPositionLog which will convert using our adapter functions
	resp, err := svc.RetrieveFinancialPositionLog(context.Background(), &positionkeepingv1.RetrieveFinancialPositionLogRequest{
		LogId: log.LogID.String(),
	})

	if err != nil || resp == nil {
		return nil
	}

	return resp.Log
}

// Helper functions for creating test data

func mustNewMoney(amount string, currency domain.Currency) domain.Money {
	dec, err := decimal.NewFromString(amount)
	if err != nil {
		panic(err)
	}
	money, err := domain.NewMoney(dec, currency)
	if err != nil {
		panic(err)
	}
	return money
}

func mustNewTransactionLineage(
	transactionID uuid.UUID,
	transactionType string,
	parentTransactionID *uuid.UUID,
	childTransactionIDs []uuid.UUID,
	relatedTransactionIDs []uuid.UUID,
) *domain.TransactionLineage {
	lineage, err := domain.NewTransactionLineage(
		transactionID,
		transactionType,
		parentTransactionID,
		childTransactionIDs,
		relatedTransactionIDs,
	)
	if err != nil {
		panic(err)
	}
	return lineage
}
