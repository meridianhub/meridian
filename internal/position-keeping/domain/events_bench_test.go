// Package domain_test provides performance benchmarks for domain event operations.
//
// These benchmarks measure event creation and serialization performance.
// Target metrics:
//   - Event creation: <1 µs/op
//   - Protobuf serialization: <500 ns/op for single events
//   - Bulk operations: Linear scaling from 10 to 10,000 events
//
// Bulk event tests (10, 100, 1000, 10000) verify that event handling scales
// linearly rather than exponentially, which is critical for high-throughput
// transaction processing.
//
// Run with: go test -bench=BenchmarkEvent -benchmem -benchtime=10s
package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/position-keeping/domain"
	"github.com/shopspring/decimal"
)

// BenchmarkNewTransactionCaptured benchmarks creating TransactionCaptured events.
func BenchmarkNewTransactionCaptured(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &domain.TransactionCaptured{
			LogID:         uuid.New(),
			AccountID:     "ACC-001",
			TransactionID: uuid.New(),
			Amount:        money,
			Direction:     domain.PostingDirectionDebit,
			Source:        domain.TransactionSourceManual,
			Description:   "Test transaction",
			Reference:     "REF-001",
			CorrelationID: "CORR-001",
			Timestamp:     time.Now().UTC(),
			Version:       1,
		}
	}
}

// BenchmarkNewBulkTransactionCaptured benchmarks creating BulkTransactionCaptured events.
func BenchmarkNewBulkTransactionCaptured(b *testing.B) {
	tests := []struct {
		name  string
		count int
	}{
		{"small_10", 10},
		{"medium_100", 100},
		{"large_1000", 1000},
		{"xlarge_10000", 10000},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			logIDs := make([]uuid.UUID, tt.count)
			for i := 0; i < tt.count; i++ {
				logIDs[i] = uuid.New()
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = &domain.BulkTransactionCaptured{
					BatchID:          uuid.New(),
					TransactionCount: int32(tt.count),
					LogIDs:           logIDs,
					Source:           domain.TransactionSourceImported,
					CorrelationID:    "BULK-CORR-001",
					Timestamp:        time.Now().UTC(),
					Version:          1,
				}
			}
		})
	}
}

// BenchmarkTransactionCapturedToProto benchmarks converting TransactionCaptured to protobuf.
func BenchmarkTransactionCapturedToProto(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	event := &domain.TransactionCaptured{
		LogID:         uuid.New(),
		AccountID:     "ACC-001",
		TransactionID: uuid.New(),
		Amount:        money,
		Direction:     domain.PostingDirectionDebit,
		Source:        domain.TransactionSourceManual,
		Description:   "Test transaction",
		Reference:     "REF-001",
		CorrelationID: "CORR-001",
		Timestamp:     time.Now().UTC(),
		Version:       1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = event.ToProto()
	}
}

// BenchmarkBulkTransactionCapturedToProto benchmarks converting BulkTransactionCaptured to protobuf.
func BenchmarkBulkTransactionCapturedToProto(b *testing.B) {
	tests := []struct {
		name  string
		count int
	}{
		{"small_10", 10},
		{"medium_100", 100},
		{"large_1000", 1000},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			logIDs := make([]uuid.UUID, tt.count)
			for i := 0; i < tt.count; i++ {
				logIDs[i] = uuid.New()
			}

			event := &domain.BulkTransactionCaptured{
				BatchID:          uuid.New(),
				TransactionCount: int32(tt.count),
				LogIDs:           logIDs,
				Source:           domain.TransactionSourceImported,
				CorrelationID:    "BULK-CORR-001",
				Timestamp:        time.Now().UTC(),
				Version:          1,
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = event.ToProto()
			}
		})
	}
}

// BenchmarkEventMetadata benchmarks accessing event metadata.
func BenchmarkEventMetadata(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	event := &domain.TransactionCaptured{
		LogID:         uuid.New(),
		AccountID:     "ACC-001",
		TransactionID: uuid.New(),
		Amount:        money,
		Direction:     domain.PostingDirectionDebit,
		Source:        domain.TransactionSourceManual,
		Description:   "Test transaction",
		Reference:     "REF-001",
		CorrelationID: "CORR-001",
		Timestamp:     time.Now().UTC(),
		Version:       1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = event.EventType()
		_ = event.AggregateID()
		_ = event.OccurredAt()
	}
}

// BenchmarkNewTransactionReconciled benchmarks creating TransactionReconciled events.
func BenchmarkNewTransactionReconciled(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &domain.TransactionReconciled{
			LogID:                uuid.New(),
			AccountID:            "ACC-001",
			ReconciliationStatus: domain.ReconciliationStatusMatched,
			Reason:               "System reconciliation",
			ReconciledBy:         "system",
			CorrelationID:        "CORR-001",
			Timestamp:            time.Now().UTC(),
			Version:              1,
		}
	}
}

// BenchmarkNewTransactionPosted benchmarks creating TransactionPosted events.
func BenchmarkNewTransactionPosted(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &domain.TransactionPosted{
			LogID:            uuid.New(),
			AccountID:        "ACC-001",
			PostingReference: "POST-REF-001",
			Reason:           "Posted to ledger",
			PostedBy:         "system",
			CorrelationID:    "CORR-001",
			Timestamp:        time.Now().UTC(),
			Version:          1,
		}
	}
}

// BenchmarkNewTransactionRejected benchmarks creating TransactionRejected events.
func BenchmarkNewTransactionRejected(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = &domain.TransactionRejected{
			LogID:         uuid.New(),
			AccountID:     "ACC-001",
			Reason:        "Validation failed",
			RejectedBy:    "system",
			CorrelationID: "CORR-001",
			Timestamp:     time.Now().UTC(),
			Version:       1,
		}
	}
}

// BenchmarkEventTypeSwitch benchmarks a typical event type switch operation.
func BenchmarkEventTypeSwitch(b *testing.B) {
	events := []domain.DomainEvent{}

	// Create mix of events
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)

	capturedEvent := &domain.TransactionCaptured{
		LogID:         uuid.New(),
		AccountID:     "ACC-001",
		TransactionID: uuid.New(),
		Amount:        money,
		Direction:     domain.PostingDirectionDebit,
		Source:        domain.TransactionSourceManual,
		Description:   "Test transaction",
		Reference:     "REF-001",
		CorrelationID: "CORR-001",
		Timestamp:     time.Now().UTC(),
		Version:       1,
	}
	events = append(events, capturedEvent)

	reconciledEvent := &domain.TransactionReconciled{
		LogID:                uuid.New(),
		AccountID:            "ACC-001",
		ReconciliationStatus: domain.ReconciliationStatusMatched,
		Reason:               "System reconciliation",
		ReconciledBy:         "system",
		CorrelationID:        "CORR-001",
		Timestamp:            time.Now().UTC(),
		Version:              1,
	}
	events = append(events, reconciledEvent)

	postedEvent := &domain.TransactionPosted{
		LogID:            uuid.New(),
		AccountID:        "ACC-001",
		PostingReference: "POST-REF-001",
		Reason:           "Posted to ledger",
		PostedBy:         "system",
		CorrelationID:    "CORR-001",
		Timestamp:        time.Now().UTC(),
		Version:          1,
	}
	events = append(events, postedEvent)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, event := range events {
			switch event.EventType() {
			case "position_keeping.transaction_captured.v1":
				_ = event.AggregateID()
			case "position_keeping.transaction_reconciled.v1":
				_ = event.AggregateID()
			case "position_keeping.transaction_posted.v1":
				_ = event.AggregateID()
			}
		}
	}
}
