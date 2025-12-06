// Package persistence provides performance benchmarks for PostgreSQL repository operations.
//
// These benchmarks measure repository persistence operations with real PostgreSQL instances.
// Target metrics:
//   - Single posting save: P99 < 20ms
//   - Batch save (10 postings): P99 < 50ms
//   - Query by booking log ID: P99 < 10ms
//   - List with filters: P99 < 50ms
//
// Run with: go test -bench=BenchmarkRepository -benchmem -benchtime=10s
package persistence

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// benchTestContainer holds test infrastructure for benchmarks
type benchTestContainer struct {
	db      *gorm.DB
	repo    *LedgerRepository
	cleanup func()
}

// setupBenchContainer creates a test container for benchmarking.
func setupBenchContainer(b *testing.B) *benchTestContainer {
	b.Helper()

	db, cleanup := testdb.SetupPostgres(&testing.T{}, []interface{}{
		&LedgerPostingEntity{},
		&FinancialBookingLogEntity{},
	})

	repo := NewLedgerRepository(db)

	b.Cleanup(func() {
		cleanup()
	})

	return &benchTestContainer{
		db:      db,
		repo:    repo,
		cleanup: cleanup,
	}
}

// createBenchPosting creates a realistic ledger posting for benchmarking.
func createBenchPosting(b *testing.B, bookingLogID uuid.UUID, direction domain.PostingDirection, accountID string) *domain.LedgerPosting {
	b.Helper()

	money, err := domain.NewMoney(decimal.NewFromFloat(100.50), domain.CurrencyGBP)
	if err != nil {
		b.Fatal(err)
	}

	posting, err := domain.NewLedgerPosting(
		bookingLogID,
		direction,
		money,
		accountID,
		time.Now().UTC(),
		fmt.Sprintf("corr-%s", uuid.New().String()[:8]),
	)
	if err != nil {
		b.Fatal(err)
	}

	if err := posting.Post("benchmark"); err != nil {
		b.Fatal(err)
	}

	return posting
}

// BenchmarkSavePosting_Single benchmarks saving a single posting.
// Target: P99 < 20ms
func BenchmarkSavePosting_Single(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bookingLogID := uuid.New()
		posting := createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, fmt.Sprintf("ACC-%08d", i))
		b.StartTimer()

		err := tc.repo.SavePosting(ctx, posting)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSavePostingsInTransaction_Small benchmarks batch creation with 2 postings.
// This represents typical double-entry bookkeeping (debit + credit).
func BenchmarkSavePostingsInTransaction_Small(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bookingLogID := uuid.New()
		postings := []*domain.LedgerPosting{
			createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, fmt.Sprintf("ACC-D-%08d", i)),
			createBenchPosting(b, bookingLogID, domain.PostingDirectionCredit, fmt.Sprintf("ACC-C-%08d", i)),
		}
		b.StartTimer()

		err := tc.repo.SavePostingsInTransaction(ctx, postings)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSavePostingsInTransaction_Medium benchmarks batch creation with 10 postings.
func BenchmarkSavePostingsInTransaction_Medium(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	batchSize := 10

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bookingLogID := uuid.New()
		postings := make([]*domain.LedgerPosting, batchSize)
		for j := 0; j < batchSize; j++ {
			direction := domain.PostingDirectionDebit
			if j%2 == 1 {
				direction = domain.PostingDirectionCredit
			}
			postings[j] = createBenchPosting(b, bookingLogID, direction, fmt.Sprintf("ACC-%d-%d", i, j))
		}
		b.StartTimer()

		err := tc.repo.SavePostingsInTransaction(ctx, postings)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSavePostingsInTransaction_Large benchmarks batch creation with 100 postings.
func BenchmarkSavePostingsInTransaction_Large(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	batchSize := 100

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bookingLogID := uuid.New()
		postings := make([]*domain.LedgerPosting, batchSize)
		for j := 0; j < batchSize; j++ {
			direction := domain.PostingDirectionDebit
			if j%2 == 1 {
				direction = domain.PostingDirectionCredit
			}
			postings[j] = createBenchPosting(b, bookingLogID, direction, fmt.Sprintf("ACC-%d-%d", i, j))
		}
		b.StartTimer()

		err := tc.repo.SavePostingsInTransaction(ctx, postings)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetPosting benchmarks retrieving a single posting by ID.
func BenchmarkGetPosting(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create a posting to retrieve
	bookingLogID := uuid.New()
	posting := createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, "ACC-GET-TEST")
	if err := tc.repo.SavePosting(ctx, posting); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.repo.GetPosting(ctx, posting.ID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetPostingsByBookingLogID benchmarks retrieving all postings for a booking log.
// Target: P99 < 10ms
func BenchmarkGetPostingsByBookingLogID(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	bookingLogID := uuid.New()

	// Setup: create multiple postings for the same booking log
	for i := 0; i < 10; i++ {
		direction := domain.PostingDirectionDebit
		if i%2 == 1 {
			direction = domain.PostingDirectionCredit
		}
		posting := createBenchPosting(b, bookingLogID, direction, fmt.Sprintf("ACC-GROUP-%d", i))
		if err := tc.repo.SavePosting(ctx, posting); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		postings, err := tc.repo.GetPostingsByBookingLogID(ctx, bookingLogID)
		if err != nil {
			b.Fatal(err)
		}
		if len(postings) != 10 {
			b.Fatalf("expected 10 postings, got %d", len(postings))
		}
	}
}

// BenchmarkGetPostingsByBookingLogID_LargeResultSet benchmarks retrieval with many postings.
func BenchmarkGetPostingsByBookingLogID_LargeResultSet(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	bookingLogID := uuid.New()

	// Setup: create 100 postings for the same booking log
	for i := 0; i < 100; i++ {
		direction := domain.PostingDirectionDebit
		if i%2 == 1 {
			direction = domain.PostingDirectionCredit
		}
		posting := createBenchPosting(b, bookingLogID, direction, fmt.Sprintf("ACC-LARGE-%d", i))
		if err := tc.repo.SavePosting(ctx, posting); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		postings, err := tc.repo.GetPostingsByBookingLogID(ctx, bookingLogID)
		if err != nil {
			b.Fatal(err)
		}
		if len(postings) != 100 {
			b.Fatalf("expected 100 postings, got %d", len(postings))
		}
	}
}

// BenchmarkUpdatePosting benchmarks updating an existing posting.
func BenchmarkUpdatePosting(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a fresh posting for each update
		bookingLogID := uuid.New()
		posting := createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, fmt.Sprintf("ACC-UPDATE-%d", i))
		if err := tc.repo.SavePosting(ctx, posting); err != nil {
			b.Fatal(err)
		}

		// Modify the posting
		posting.PostingResult = "Updated result"
		b.StartTimer()

		err := tc.repo.UpdatePosting(ctx, posting)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListPostings benchmarks paginated list queries with various filters.
// Target: P99 < 50ms
func BenchmarkListPostings(b *testing.B) {
	testCases := []struct {
		name   string
		params ListPostingsParams
	}{
		{
			name: "NoFilter",
			params: ListPostingsParams{
				PageSize: 50,
			},
		},
		{
			name: "FilterByDirection",
			params: ListPostingsParams{
				PostingDirection: "DEBIT",
				PageSize:         50,
			},
		},
		{
			name: "FilterByCurrency",
			params: ListPostingsParams{
				Currency: "GBP",
				PageSize: 50,
			},
		},
		{
			name: "FilterByStatus",
			params: ListPostingsParams{
				Status:   "POSTED",
				PageSize: 50,
			},
		},
		{
			name: "SmallPage",
			params: ListPostingsParams{
				PageSize: 10,
			},
		},
		{
			name: "LargePage",
			params: ListPostingsParams{
				PageSize: 100,
			},
		},
	}

	for _, testCase := range testCases {
		b.Run(testCase.name, func(b *testing.B) {
			tc := setupBenchContainer(b)
			ctx := context.Background()

			// Setup data for this sub-benchmark
			for i := 0; i < 100; i++ {
				bookingLogID := uuid.New()
				direction := domain.PostingDirectionDebit
				if i%2 == 1 {
					direction = domain.PostingDirectionCredit
				}
				posting := createBenchPosting(b, bookingLogID, direction, fmt.Sprintf("ACC-LIST-%08d", i))
				if err := tc.repo.SavePosting(ctx, posting); err != nil {
					b.Fatal(err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := tc.repo.ListPostings(ctx, testCase.params)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkListPostings_LargeDataset benchmarks list queries against a large dataset.
func BenchmarkListPostings_LargeDataset(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create 1000 postings
	for i := 0; i < 1000; i++ {
		bookingLogID := uuid.New()
		direction := domain.PostingDirectionDebit
		if i%2 == 1 {
			direction = domain.PostingDirectionCredit
		}
		posting := createBenchPosting(b, bookingLogID, direction, fmt.Sprintf("ACC-LARGE-DS-%08d", i))
		if err := tc.repo.SavePosting(ctx, posting); err != nil {
			b.Fatal(err)
		}
	}

	params := ListPostingsParams{
		PageSize: 50,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.repo.ListPostings(ctx, params)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListBookingLogs benchmarks listing booking logs.
func BenchmarkListBookingLogs(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create booking logs
	for i := 0; i < 100; i++ {
		entity := &FinancialBookingLogEntity{
			ID:                      uuid.New(),
			FinancialAccountType:    "DEBIT",
			ProductServiceReference: fmt.Sprintf("PROD-%d", i),
			BusinessUnitReference:   fmt.Sprintf("BU-%d", i%5),
			ChartOfAccountsRules:    "{}",
			BaseCurrency:            "GBP",
			Status:                  "ACTIVE",
			IdempotencyKey:          fmt.Sprintf("idem-bench-%s", uuid.New().String()),
			CreatedAt:               time.Now(),
			UpdatedAt:               time.Now(),
			Version:                 1,
		}
		if err := tc.db.Create(entity).Error; err != nil {
			b.Fatal(err)
		}
	}

	testCases := []struct {
		name   string
		params ListBookingLogsParams
	}{
		{
			name: "NoFilter",
			params: ListBookingLogsParams{
				PageSize: 50,
			},
		},
		{
			name: "FilterByStatus",
			params: ListBookingLogsParams{
				StatusFilter: "ACTIVE",
				PageSize:     50,
			},
		},
		{
			name: "FilterByBusinessUnit",
			params: ListBookingLogsParams{
				BusinessUnitFilter: "BU-0",
				PageSize:           50,
			},
		},
	}

	for _, testCase := range testCases {
		b.Run(testCase.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := tc.repo.ListBookingLogs(ctx, testCase.params)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConcurrentReads benchmarks concurrent read operations.
func BenchmarkConcurrentReads(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create test data
	bookingLogID := uuid.New()
	posting := createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, "ACC-CONCURRENT-READ")
	if err := tc.repo.SavePosting(ctx, posting); err != nil {
		b.Fatal(err)
	}

	var hasError atomic.Bool
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if hasError.Load() {
				return
			}
			_, err := tc.repo.GetPosting(ctx, posting.ID)
			if err != nil {
				hasError.Store(true)
				b.Error(err)
				return
			}
		}
	})
}

// BenchmarkConcurrentWrites benchmarks concurrent write operations.
func BenchmarkConcurrentWrites(b *testing.B) {
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
			bookingLogID := uuid.New()
			posting := createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, fmt.Sprintf("ACC-CONC-%08d", n))
			if err := tc.repo.SavePosting(ctx, posting); err != nil {
				hasError.Store(true)
				b.Error(err)
				return
			}
		}
	})
}

// BenchmarkMixedWorkload benchmarks a realistic mix of operations.
// 60% reads, 30% creates, 10% updates.
func BenchmarkMixedWorkload(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: pre-populate some data
	var postingIDs []uuid.UUID
	for i := 0; i < 100; i++ {
		bookingLogID := uuid.New()
		posting := createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, fmt.Sprintf("ACC-MIXED-%08d", i))
		if err := tc.repo.SavePosting(ctx, posting); err != nil {
			b.Fatal(err)
		}
		postingIDs = append(postingIDs, posting.ID)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		operation := i % 10

		switch {
		case operation < 6: // 60% reads
			idx := i % len(postingIDs)
			_, err := tc.repo.GetPosting(ctx, postingIDs[idx])
			if err != nil {
				b.Fatal(err)
			}

		case operation < 9: // 30% creates
			bookingLogID := uuid.New()
			posting := createBenchPosting(b, bookingLogID, domain.PostingDirectionDebit, fmt.Sprintf("ACC-NEW-%08d", i))
			if err := tc.repo.SavePosting(ctx, posting); err != nil {
				b.Fatal(err)
			}

		default: // 10% updates
			idx := i % len(postingIDs)
			posting, err := tc.repo.GetPosting(ctx, postingIDs[idx])
			if err != nil {
				b.Fatal(err)
			}
			posting.PostingResult = fmt.Sprintf("Updated at iteration %d", i)
			if err := tc.repo.UpdatePosting(ctx, posting); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkTransactionThroughput measures maximum transaction throughput.
// Reports postings/sec as a custom metric to measure bulk insert performance.
func BenchmarkTransactionThroughput(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	batchSize := 100

	b.ReportAllocs()
	b.ResetTimer()

	totalPostings := 0
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bookingLogID := uuid.New()
		postings := make([]*domain.LedgerPosting, batchSize)
		for j := 0; j < batchSize; j++ {
			direction := domain.PostingDirectionDebit
			if j%2 == 1 {
				direction = domain.PostingDirectionCredit
			}
			postings[j] = createBenchPosting(b, bookingLogID, direction, fmt.Sprintf("ACC-THRU-%d-%d", i, j))
		}
		b.StartTimer()

		err := tc.repo.SavePostingsInTransaction(ctx, postings)
		if err != nil {
			b.Fatal(err)
		}
		totalPostings += batchSize
	}

	b.StopTimer()
	// Report postings per second as a custom metric
	if b.N > 0 {
		duration := b.Elapsed()
		if duration.Seconds() > 0 {
			postingsPerSec := float64(totalPostings) / duration.Seconds()
			b.ReportMetric(postingsPerSec, "postings/sec")
		}
	}
}
