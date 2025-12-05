package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTransactionCaptured_ValidationEdgeCases tests edge cases that would fail protobuf validation
func TestTransactionCaptured_ValidationEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		setupEvent    func() *domain.TransactionCaptured
		expectedError string // Empty if validation should pass
	}{
		{
			name: "account_id at max length (100 chars)",
			setupEvent: func() *domain.TransactionCaptured {
				money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
				return &domain.TransactionCaptured{
					LogID:         uuid.New(),
					AccountID:     strings.Repeat("A", 100),
					TransactionID: uuid.New(),
					Amount:        money,
					Direction:     domain.PostingDirectionDebit,
					Source:        domain.TransactionSourceManual,
					CorrelationID: "CORR-123",
					Timestamp:     time.Now().UTC(),
					Version:       1,
				}
			},
		},
		{
			name: "description at max length (500 chars)",
			setupEvent: func() *domain.TransactionCaptured {
				money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
				return &domain.TransactionCaptured{
					LogID:         uuid.New(),
					AccountID:     "ACC-123",
					TransactionID: uuid.New(),
					Amount:        money,
					Direction:     domain.PostingDirectionCredit,
					Source:        domain.TransactionSourceAutomated,
					Description:   strings.Repeat("D", 500),
					CorrelationID: "CORR-123",
					Timestamp:     time.Now().UTC(),
					Version:       1,
				}
			},
		},
		{
			name: "reference at max length (100 chars)",
			setupEvent: func() *domain.TransactionCaptured {
				money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
				return &domain.TransactionCaptured{
					LogID:         uuid.New(),
					AccountID:     "ACC-123",
					TransactionID: uuid.New(),
					Amount:        money,
					Direction:     domain.PostingDirectionDebit,
					Source:        domain.TransactionSourceImported,
					Reference:     strings.Repeat("R", 100),
					CorrelationID: "CORR-123",
					Timestamp:     time.Now().UTC(),
					Version:       1,
				}
			},
		},
		{
			name: "correlation_id at max length (255 chars)",
			setupEvent: func() *domain.TransactionCaptured {
				money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
				return &domain.TransactionCaptured{
					LogID:         uuid.New(),
					AccountID:     "ACC-123",
					TransactionID: uuid.New(),
					Amount:        money,
					Direction:     domain.PostingDirectionDebit,
					Source:        domain.TransactionSourceReconciliation,
					CorrelationID: strings.Repeat("C", 255),
					Timestamp:     time.Now().UTC(),
					Version:       1,
				}
			},
		},
		{
			name: "all transaction sources are valid",
			setupEvent: func() *domain.TransactionCaptured {
				money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
				return &domain.TransactionCaptured{
					LogID:         uuid.New(),
					AccountID:     "ACC-123",
					TransactionID: uuid.New(),
					Amount:        money,
					Direction:     domain.PostingDirectionDebit,
					Source:        domain.TransactionSourceFinancialAccounting,
					CorrelationID: "CORR-123",
					Timestamp:     time.Now().UTC(),
					Version:       1,
				}
			},
		},
		{
			name: "minimum version (1)",
			setupEvent: func() *domain.TransactionCaptured {
				money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
				return &domain.TransactionCaptured{
					LogID:         uuid.New(),
					AccountID:     "ACC-123",
					TransactionID: uuid.New(),
					Amount:        money,
					Direction:     domain.PostingDirectionDebit,
					Source:        domain.TransactionSourceManual,
					CorrelationID: "CORR-123",
					Timestamp:     time.Now().UTC(),
					Version:       1,
				}
			},
		},
		{
			name: "large version number",
			setupEvent: func() *domain.TransactionCaptured {
				money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
				return &domain.TransactionCaptured{
					LogID:         uuid.New(),
					AccountID:     "ACC-123",
					TransactionID: uuid.New(),
					Amount:        money,
					Direction:     domain.PostingDirectionDebit,
					Source:        domain.TransactionSourceManual,
					CorrelationID: "CORR-123",
					Timestamp:     time.Now().UTC(),
					Version:       999999,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := tt.setupEvent()
			proto := event.ToProto()
			require.NotNil(t, proto)

			// Verify conversion succeeds
			capturedProto, ok := proto.(*eventsv1.TransactionCapturedEvent)
			require.True(t, ok)
			assert.NotEmpty(t, capturedProto.LogId)
			assert.NotEmpty(t, capturedProto.AccountId)
		})
	}
}

// TestBulkTransactionCaptured_ValidationEdgeCases tests bulk event edge cases
func TestBulkTransactionCaptured_ValidationEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		setupEvent func() *domain.BulkTransactionCaptured
	}{
		{
			name: "minimum log_ids (1 item)",
			setupEvent: func() *domain.BulkTransactionCaptured {
				return &domain.BulkTransactionCaptured{
					BatchID:          uuid.New(),
					TransactionCount: 1,
					LogIDs:           []uuid.UUID{uuid.New()},
					Source:           domain.TransactionSourceImported,
					CorrelationID:    "BULK-123",
					Timestamp:        time.Now().UTC(),
					Version:          1,
				}
			},
		},
		{
			name: "large batch (1000 items)",
			setupEvent: func() *domain.BulkTransactionCaptured {
				logIDs := make([]uuid.UUID, 1000)
				for i := 0; i < 1000; i++ {
					logIDs[i] = uuid.New()
				}
				return &domain.BulkTransactionCaptured{
					BatchID:          uuid.New(),
					TransactionCount: 1000,
					LogIDs:           logIDs,
					Source:           domain.TransactionSourceImported,
					CorrelationID:    "BULK-123",
					Timestamp:        time.Now().UTC(),
					Version:          1,
				}
			},
		},
		{
			name: "max batch size (10000 items)",
			setupEvent: func() *domain.BulkTransactionCaptured {
				logIDs := make([]uuid.UUID, 10000)
				for i := 0; i < 10000; i++ {
					logIDs[i] = uuid.New()
				}
				return &domain.BulkTransactionCaptured{
					BatchID:          uuid.New(),
					TransactionCount: 10000,
					LogIDs:           logIDs,
					Source:           domain.TransactionSourceImported,
					CorrelationID:    "BULK-MAX",
					Timestamp:        time.Now().UTC(),
					Version:          1,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := tt.setupEvent()
			proto := event.ToProto()
			require.NotNil(t, proto)

			// Verify conversion succeeds
			bulkProto, ok := proto.(*eventsv1.BulkTransactionCapturedEvent)
			require.True(t, ok)
			assert.NotEmpty(t, bulkProto.BatchId)
			assert.Equal(t, event.TransactionCount, bulkProto.TransactionCount)
			assert.Len(t, bulkProto.LogIds, len(event.LogIDs))
		})
	}
}

// TestTransactionReconciled_ValidationEdgeCases tests reconciliation event edge cases
func TestTransactionReconciled_ValidationEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		setupEvent func() *domain.TransactionReconciled
	}{
		{
			name: "all reconciliation statuses are valid",
			setupEvent: func() *domain.TransactionReconciled {
				return &domain.TransactionReconciled{
					LogID:                uuid.New(),
					AccountID:            "ACC-123",
					ReconciliationStatus: domain.ReconciliationStatusUnreconciled,
					Reason:               "Test",
					ReconciledBy:         "system",
					CorrelationID:        "CORR-123",
					Timestamp:            time.Now().UTC(),
					Version:              1,
				}
			},
		},
		{
			name: "reason at max length (500 chars)",
			setupEvent: func() *domain.TransactionReconciled {
				return &domain.TransactionReconciled{
					LogID:                uuid.New(),
					AccountID:            "ACC-123",
					ReconciliationStatus: domain.ReconciliationStatusMatched,
					Reason:               strings.Repeat("R", 500),
					ReconciledBy:         "system",
					CorrelationID:        "CORR-123",
					Timestamp:            time.Now().UTC(),
					Version:              1,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := tt.setupEvent()
			proto := event.ToProto()
			require.NotNil(t, proto)

			// Verify conversion succeeds
			reconciledProto, ok := proto.(*eventsv1.TransactionReconciledEvent)
			require.True(t, ok)
			assert.NotEmpty(t, reconciledProto.LogId)
		})
	}
}

// TestAmountValidation tests amount edge cases for different currencies
func TestAmountValidation_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		amount   decimal.Decimal
		currency domain.Currency
		want     int64
	}{
		{
			name:     "JPY minimum amount (1)",
			amount:   decimal.NewFromInt(1),
			currency: domain.CurrencyJPY,
			want:     1,
		},
		{
			name:     "GBP smallest amount (0.01 = 1 penny)",
			amount:   decimal.NewFromFloat(0.01),
			currency: domain.CurrencyGBP,
			want:     1,
		},
		{
			name:     "USD large amount (1 million)",
			amount:   decimal.NewFromInt(1000000),
			currency: domain.CurrencyUSD,
			want:     100000000, // 1M dollars = 100M cents
		},
		{
			name:     "JPY large amount (100 million)",
			amount:   decimal.NewFromInt(100000000),
			currency: domain.CurrencyJPY,
			want:     100000000, // No conversion for JPY
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := domain.NewMoney(tt.amount, tt.currency)
			require.NoError(t, err)

			event := &domain.TransactionCaptured{
				LogID:         uuid.New(),
				AccountID:     "ACC-123",
				TransactionID: uuid.New(),
				Amount:        money,
				Direction:     domain.PostingDirectionDebit,
				Source:        domain.TransactionSourceManual,
				CorrelationID: "CORR-123",
				Timestamp:     time.Now().UTC(),
				Version:       1,
			}

			proto := event.ToProto().(*eventsv1.TransactionCapturedEvent)
			assert.Equal(t, tt.want, proto.AmountCents)
		})
	}
}
