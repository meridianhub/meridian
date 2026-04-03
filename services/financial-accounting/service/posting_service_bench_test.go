// Package service provides performance benchmarks for ledger posting operations.
//
// These benchmarks measure service-layer operations with real PostgreSQL instances.
// Target metrics:
//   - Single deposit processing: P99 < 50ms (includes double-entry creation)
//   - Double-entry validation: P99 < 10ms
//   - Batch retrieval: <1ms per posting
//
// Run with: go test -bench=BenchmarkPosting -benchmem -benchtime=10s
package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// benchTestContainer holds test infrastructure for benchmarks
type benchTestContainer struct {
	db      *gorm.DB
	repo    *persistence.LedgerRepository
	service *PostingService
	cleanup func()
}

// setupBenchContainer creates a test container for benchmarking.
// The container is reused across benchmark iterations to avoid setup overhead.
func setupBenchContainer(b *testing.B) *benchTestContainer {
	b.Helper()

	// Use testing.T for setup since testdb expects *testing.T
	db, cleanup := testdb.SetupPostgres(&testing.T{}, []interface{}{
		&persistence.LedgerPostingEntity{},
		&persistence.FinancialBookingLogEntity{},
	})

	repo := persistence.NewLedgerRepository(db)
	service := NewPostingService(repo, "BANK-CASH-BENCH")

	b.Cleanup(func() {
		cleanup()
	})

	return &benchTestContainer{
		db:      db,
		repo:    repo,
		service: service,
		cleanup: cleanup,
	}
}

// createBenchDepositEvent creates a realistic deposit event for benchmarking.
func createBenchDepositEvent(i int) DepositEvent {
	// Vary amounts: base 100.00 + cents variation
	amount := decimal.NewFromInt(int64(10000 + (i % 100000))).Div(decimal.NewFromInt(100))
	return DepositEvent{
		AccountID:      fmt.Sprintf("ACC-BENCH-%08d", i),
		Amount:         amount.String(),
		InstrumentCode: "GBP",
		CorrelationID:  fmt.Sprintf("deposit-bench-%s", uuid.New().String()[:8]),
		ValueDate:      time.Now().UTC(),
	}
}

// BenchmarkProcessDeposit_Single benchmarks processing a single deposit.
// This measures the full double-entry creation flow including:
//   - Money creation and validation
//   - Debit/credit posting creation
//   - Atomic transaction save
//
// Target: P99 < 50ms
func BenchmarkProcessDeposit_Single(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := createBenchDepositEvent(i)
		err := tc.service.ProcessDeposit(ctx, event)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProcessDeposit_Parallel benchmarks concurrent deposit processing.
// This tests the system's ability to handle concurrent double-entry transactions.
func BenchmarkProcessDeposit_Parallel(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	var counter atomic.Int64
	var hasError atomic.Bool
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if hasError.Load() {
				return
			}
			n := counter.Add(1)
			event := createBenchDepositEvent(int(n))
			err := tc.service.ProcessDeposit(ctx, event)
			if err != nil {
				hasError.Store(true)
				b.Error(err)
				return
			}
		}
	})
}

// BenchmarkProcessDeposit_VariedAmounts benchmarks deposits with different amounts.
// Tests that amount processing performance is consistent across value ranges.
func BenchmarkProcessDeposit_VariedAmounts(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	amounts := []string{
		"1.00",         // £1.00
		"100.00",       // £100.00
		"1000.00",      // £1,000.00
		"100000.00",    // £100,000.00
		"100000000.00", // £100,000,000.00
	}

	for _, amount := range amounts {
		b.Run(fmt.Sprintf("Amount_%s", amount), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				event := DepositEvent{
					AccountID:      fmt.Sprintf("ACC-VAR-%08d", i),
					Amount:         amount,
					InstrumentCode: "GBP",
					CorrelationID:  fmt.Sprintf("deposit-var-%s", uuid.New().String()[:8]),
					ValueDate:      time.Now().UTC(),
				}
				err := tc.service.ProcessDeposit(ctx, event)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkValidateDoubleEntry benchmarks double-entry validation.
// Target: P99 < 10ms
func BenchmarkValidateDoubleEntry(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: Create a deposit to validate
	event := createBenchDepositEvent(0)
	err := tc.service.ProcessDeposit(ctx, event)
	if err != nil {
		b.Fatal(err)
	}

	// Get the booking log ID from the created posting
	var entity persistence.LedgerPostingEntity
	if err := tc.db.First(&entity).Error; err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		balanced, err := tc.service.ValidateDoubleEntry(ctx, entity.FinancialBookingLogID)
		if err != nil {
			b.Fatal(err)
		}
		if !balanced {
			b.Fatal("expected balanced double entry")
		}
	}
}

// BenchmarkValidateDoubleEntry_MultiplePostings benchmarks validation with many postings.
// Tests validation performance when booking log has multiple entries.
func BenchmarkValidateDoubleEntry_MultiplePostings(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	bookingLogID := uuid.New()
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)

	// Create multiple balanced posting pairs
	for i := 0; i < 10; i++ {
		money := domain.NewMoney(decimal.NewFromInt(int64(100+i)), gbpInstrument)

		debit, err := domain.NewLedgerPosting(
			bookingLogID,
			domain.PostingDirectionDebit,
			money,
			fmt.Sprintf("ACC-DEBIT-%d", i),
			time.Now().UTC(),
			fmt.Sprintf("corr-%d", i),
		)
		if err != nil {
			b.Fatal(err)
		}
		if err := debit.Post("benchmark"); err != nil {
			b.Fatal(err)
		}
		if err := tc.repo.SavePosting(ctx, debit); err != nil {
			b.Fatal(err)
		}

		credit, err := domain.NewLedgerPosting(
			bookingLogID,
			domain.PostingDirectionCredit,
			money,
			fmt.Sprintf("ACC-CREDIT-%d", i),
			time.Now().UTC(),
			fmt.Sprintf("corr-%d", i),
		)
		if err != nil {
			b.Fatal(err)
		}
		if err := credit.Post("benchmark"); err != nil {
			b.Fatal(err)
		}
		if err := tc.repo.SavePosting(ctx, credit); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		balanced, err := tc.service.ValidateDoubleEntry(ctx, bookingLogID)
		if err != nil {
			b.Fatal(err)
		}
		if !balanced {
			b.Fatal("expected balanced double entry")
		}
	}
}

// BenchmarkGetPostingsByBookingLog benchmarks retrieval of postings for a booking log.
func BenchmarkGetPostingsByBookingLog(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: Create a deposit
	event := createBenchDepositEvent(0)
	err := tc.service.ProcessDeposit(ctx, event)
	if err != nil {
		b.Fatal(err)
	}

	// Get the booking log ID
	var entity persistence.LedgerPostingEntity
	if err := tc.db.First(&entity).Error; err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		postings, err := tc.service.GetPostingsByBookingLog(ctx, entity.FinancialBookingLogID)
		if err != nil {
			b.Fatal(err)
		}
		if len(postings) != 2 {
			b.Fatalf("expected 2 postings, got %d", len(postings))
		}
	}
}

// BenchmarkGetPostingsByBookingLog_ManyPostings benchmarks retrieval with many postings.
func BenchmarkGetPostingsByBookingLog_ManyPostings(b *testing.B) {
	testCases := []int{10, 50, 100}

	for _, postingCount := range testCases {
		b.Run(fmt.Sprintf("Postings_%d", postingCount), func(b *testing.B) {
			tc := setupBenchContainer(b)
			ctx := context.Background()

			bookingLogID := uuid.New()
			gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)

			// Create many postings for the same booking log
			for i := 0; i < postingCount; i++ {
				money := domain.NewMoney(decimal.NewFromInt(int64(100)), gbpInstrument)
				direction := domain.PostingDirectionDebit
				if i%2 == 1 {
					direction = domain.PostingDirectionCredit
				}

				posting, err := domain.NewLedgerPosting(
					bookingLogID,
					direction,
					money,
					fmt.Sprintf("ACC-%d", i),
					time.Now().UTC(),
					fmt.Sprintf("corr-%d", i),
				)
				if err != nil {
					b.Fatal(err)
				}
				if err := posting.Post("benchmark"); err != nil {
					b.Fatal(err)
				}
				if err := tc.repo.SavePosting(ctx, posting); err != nil {
					b.Fatal(err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				postings, err := tc.service.GetPostingsByBookingLog(ctx, bookingLogID)
				if err != nil {
					b.Fatal(err)
				}
				if len(postings) != postingCount {
					b.Fatalf("expected %d postings, got %d", postingCount, len(postings))
				}
			}
		})
	}
}

// BenchmarkMixedServiceWorkload benchmarks a realistic mix of service operations.
// 50% deposit processing, 30% retrieval, 20% validation
func BenchmarkMixedServiceWorkload(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Pre-create some deposits for retrieval/validation operations
	var bookingLogIDs []uuid.UUID
	for i := 0; i < 20; i++ {
		event := createBenchDepositEvent(i)
		if err := tc.service.ProcessDeposit(ctx, event); err != nil {
			b.Fatal(err)
		}
	}

	// Get booking log IDs from created postings
	var entities []persistence.LedgerPostingEntity
	if err := tc.db.Find(&entities).Error; err != nil {
		b.Fatal(err)
	}

	// Collect unique booking log IDs
	seen := make(map[uuid.UUID]bool)
	for _, e := range entities {
		if !seen[e.FinancialBookingLogID] {
			bookingLogIDs = append(bookingLogIDs, e.FinancialBookingLogID)
			seen[e.FinancialBookingLogID] = true
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		operation := i % 10

		switch {
		case operation < 5: // 50% deposits
			event := createBenchDepositEvent(i + 1000)
			if err := tc.service.ProcessDeposit(ctx, event); err != nil {
				b.Fatal(err)
			}

		case operation < 8: // 30% retrieval
			idx := i % len(bookingLogIDs)
			_, err := tc.service.GetPostingsByBookingLog(ctx, bookingLogIDs[idx])
			if err != nil {
				b.Fatal(err)
			}

		default: // 20% validation
			idx := i % len(bookingLogIDs)
			_, err := tc.service.ValidateDoubleEntry(ctx, bookingLogIDs[idx])
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
