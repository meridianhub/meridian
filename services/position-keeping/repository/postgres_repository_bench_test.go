// Package repository_test provides performance benchmarks for PostgreSQL repository operations.
//
// These benchmarks measure repository persistence operations with real PostgreSQL instances.
// Target metrics from requirements:
//   - Single transaction capture: P99 < 20ms
//   - Bulk import: >10,000 txn/sec
//   - Database query performance optimization
//
// Benchmark scenarios:
//   - Single operations (Create, FindByID, Update)
//   - Small batch operations (10 records)
//   - Medium batch operations (100 records)
//   - Large batch operations (1000 records)
//
// Run with: go test -bench=BenchmarkPostgres -benchmem -benchtime=10s
package repository_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
)

// setupBenchContainer creates a test container for benchmarking.
// The container is reused across benchmark iterations to avoid setup overhead.
func setupBenchContainer(b *testing.B) *testContainer {
	b.Helper()

	tc := setupTestContainer(&testing.T{})
	b.Cleanup(func() {
		tc.cleanup(&testing.T{})
	})

	return tc
}

// createBenchLog creates a realistic FinancialPositionLog for benchmarking.
func createBenchLog(b *testing.B, accountID string) *domain.FinancialPositionLog {
	b.Helper()

	amount, err := domain.NewMoney(decimal.NewFromFloat(100.50), domain.CurrencyGBP)
	if err != nil {
		b.Fatal(err)
	}

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		amount,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Benchmark transaction",
		fmt.Sprintf("REF-%s", uuid.New().String()[:8]),
		domain.TransactionSourceManual,
	)
	if err != nil {
		b.Fatal(err)
	}

	lineage, err := domain.NewTransactionLineage(
		uuid.New(),
		"payment",
		nil,
		[]uuid.UUID{},
		[]uuid.UUID{},
	)
	if err != nil {
		b.Fatal(err)
	}

	log, err := domain.NewFinancialPositionLog(accountID, entry, lineage)
	if err != nil {
		b.Fatal(err)
	}

	auditEntry, err := domain.NewAuditTrailEntry(
		"bench-user",
		"create",
		"Benchmark log created",
		"127.0.0.1",
		map[string]string{"system": "benchmark"},
	)
	if err != nil {
		b.Fatal(err)
	}

	err = log.AddAuditEntry(auditEntry)
	if err != nil {
		b.Fatal(err)
	}

	return log
}

// createBenchLogs creates a slice of realistic logs for batch benchmarking.
func createBenchLogs(b *testing.B, count int) []*domain.FinancialPositionLog {
	b.Helper()

	logs := make([]*domain.FinancialPositionLog, count)
	for i := 0; i < count; i++ {
		accountID := fmt.Sprintf("GB33BUKB2020155%08d", i)
		logs[i] = createBenchLog(b, accountID)
	}
	return logs
}

// BenchmarkCreate_Single benchmarks creating a single financial position log.
// Target: P99 < 20ms for single transaction capture.
func BenchmarkCreate_Single(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", i))
		b.StartTimer()

		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCreate_SingleWithComplexAggregate benchmarks creating a log with multiple entries and rich audit trail.
func BenchmarkCreate_SingleWithComplexAggregate(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", i))

		// Add multiple transaction entries
		for j := 0; j < 5; j++ {
			amount, _ := domain.NewMoney(decimal.NewFromFloat(50.25), domain.CurrencyGBP)
			entry, _ := domain.NewTransactionLogEntry(
				uuid.New(),
				log.AccountID,
				amount,
				domain.PostingDirectionCredit,
				time.Now().UTC(),
				fmt.Sprintf("Additional entry %d", j),
				fmt.Sprintf("REF-%d", j),
				domain.TransactionSourceAutomated,
			)
			_ = log.AddEntry(entry)
		}

		// Add multiple audit entries
		for j := 0; j < 3; j++ {
			audit, _ := domain.NewAuditTrailEntry(
				fmt.Sprintf("user-%d", j),
				fmt.Sprintf("action-%d", j),
				fmt.Sprintf("Audit entry %d", j),
				"192.168.1.1",
				map[string]string{"context": fmt.Sprintf("ctx-%d", j)},
			)
			_ = log.AddAuditEntry(audit)
		}

		b.StartTimer()
		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCreateBatch_Small benchmarks batch creation with 10 logs.
func BenchmarkCreateBatch_Small(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	batchSize := 10

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		logs := createBenchLogs(b, batchSize)
		b.StartTimer()

		err := tc.repo.CreateBatch(ctx, logs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCreateBatch_Medium benchmarks batch creation with 100 logs.
// This tests the efficiency of bulk operations for moderate data volumes.
func BenchmarkCreateBatch_Medium(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	batchSize := 100

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		logs := createBenchLogs(b, batchSize)
		b.StartTimer()

		err := tc.repo.CreateBatch(ctx, logs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCreateBatch_Large benchmarks batch creation with 1000 logs.
// Target: >10,000 txn/sec for bulk import operations.
func BenchmarkCreateBatch_Large(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	batchSize := 1000

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		logs := createBenchLogs(b, batchSize)
		b.StartTimer()

		err := tc.repo.CreateBatch(ctx, logs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFindByID benchmarks retrieving a single log by ID.
// This measures database query performance and aggregate reconstitution.
func BenchmarkFindByID(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create a log to find
	log := createBenchLog(b, "GB33BUKB20201555555555")
	err := tc.repo.Create(ctx, log)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.repo.FindByID(ctx, log.LogID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFindByID_ComplexAggregate benchmarks retrieving a log with multiple related entities.
func BenchmarkFindByID_ComplexAggregate(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create a complex log
	log := createBenchLog(b, "GB33BUKB20201555555555")

	// Add multiple entries and audit records
	for i := 0; i < 10; i++ {
		amount, _ := domain.NewMoney(decimal.NewFromFloat(50.25), domain.CurrencyGBP)
		entry, _ := domain.NewTransactionLogEntry(
			uuid.New(),
			log.AccountID,
			amount,
			domain.PostingDirectionCredit,
			time.Now().UTC(),
			fmt.Sprintf("Entry %d", i),
			fmt.Sprintf("REF-%d", i),
			domain.TransactionSourceAutomated,
		)
		_ = log.AddEntry(entry)

		audit, _ := domain.NewAuditTrailEntry(
			fmt.Sprintf("user-%d", i),
			fmt.Sprintf("action-%d", i),
			fmt.Sprintf("Audit %d", i),
			"192.168.1.1",
			map[string]string{"context": fmt.Sprintf("ctx-%d", i)},
		)
		_ = log.AddAuditEntry(audit)
	}

	err := tc.repo.Create(ctx, log)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.repo.FindByID(ctx, log.LogID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFindByAccountID benchmarks retrieving all logs for an account.
// This tests query performance with multiple results and N+1 query prevention.
func BenchmarkFindByAccountID(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	accountID := "GB33BUKB20201555555555"

	// Setup: create multiple logs for the same account
	for i := 0; i < 10; i++ {
		log := createBenchLog(b, accountID)
		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.repo.FindByAccountID(ctx, accountID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFindByAccountID_LargeResultSet benchmarks retrieving many logs for an account.
func BenchmarkFindByAccountID_LargeResultSet(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()
	accountID := "GB33BUKB20201555555555"

	// Setup: create 100 logs for the same account
	for i := 0; i < 100; i++ {
		log := createBenchLog(b, accountID)
		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.repo.FindByAccountID(ctx, accountID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUpdate benchmarks updating an existing log.
// This measures optimistic locking and aggregate persistence performance.
func BenchmarkUpdate(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create a fresh log for each update
		log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", i))
		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}

		// Modify the log
		err = log.MarkPosted("Benchmark posted", nil)
		if err != nil {
			b.Fatal(err)
		}

		b.StartTimer()
		err = tc.repo.Update(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUpdate_WithAdditionalEntries benchmarks updating a log with new transaction entries.
func BenchmarkUpdate_WithAdditionalEntries(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", i))
		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}

		// Add several new entries
		for j := 0; j < 5; j++ {
			amount, _ := domain.NewMoney(decimal.NewFromFloat(25.00), domain.CurrencyGBP)
			entry, _ := domain.NewTransactionLogEntry(
				uuid.New(),
				log.AccountID,
				amount,
				domain.PostingDirectionCredit,
				time.Now().UTC(),
				fmt.Sprintf("Update entry %d", j),
				fmt.Sprintf("REF-UPD-%d", j),
				domain.TransactionSourceAutomated,
			)
			_ = log.AddEntry(entry)
		}

		err = log.MarkPosted("Posted with entries", nil)
		if err != nil {
			b.Fatal(err)
		}

		b.StartTimer()
		err = tc.repo.Update(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkList benchmarks paginated list queries with various filters.
func BenchmarkList(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create diverse logs
	for i := 0; i < 50; i++ {
		log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", i))
		if i%2 == 0 {
			_ = log.MarkPosted("Posted", nil)
		}
		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Test various filter scenarios
	testCases := []struct {
		name   string
		filter domain.PositionLogFilter
	}{
		{
			name: "NoFilter",
			filter: domain.PositionLogFilter{
				Limit:  10,
				Offset: 0,
			},
		},
		{
			name: "FilterByStatus",
			filter: domain.PositionLogFilter{
				Status: func() *domain.TransactionStatus {
					s := domain.TransactionStatusPending
					return &s
				}(),
				Limit:  10,
				Offset: 0,
			},
		},
		{
			name: "FilterByAccountID",
			filter: domain.PositionLogFilter{
				AccountID: func() *string {
					s := "GB33BUKB20201550000000"
					return &s
				}(),
				Limit:  10,
				Offset: 0,
			},
		},
		{
			name: "LargePage",
			filter: domain.PositionLogFilter{
				Limit:  100,
				Offset: 0,
			},
		},
	}

	for _, testCase := range testCases {
		b.Run(testCase.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := tc.repo.List(ctx, testCase.filter)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkList_LargeDataset benchmarks list queries against a large dataset.
func BenchmarkList_LargeDataset(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create 1000 logs
	logs := createBenchLogs(b, 1000)
	err := tc.repo.CreateBatch(ctx, logs)
	if err != nil {
		b.Fatal(err)
	}

	filter := domain.PositionLogFilter{
		Limit:  50,
		Offset: 0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tc.repo.List(ctx, filter)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFindPendingForReconciliation benchmarks finding unreconciled pending logs.
func BenchmarkFindPendingForReconciliation(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create mix of pending and posted logs
	for i := 0; i < 100; i++ {
		log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", i))
		// Mark half as posted so they won't be in results
		if i%2 == 0 {
			_ = log.MarkPosted("Posted", nil)
		}
		err := tc.repo.Create(ctx, log)
		if err != nil {
			b.Fatal(err)
		}
	}

	testCases := []struct {
		name  string
		limit int
	}{
		{"NoLimit", 0},
		{"Limit10", 10},
		{"Limit100", 100},
	}

	for _, testCase := range testCases {
		b.Run(testCase.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := tc.repo.FindPendingForReconciliation(ctx, testCase.limit)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkConcurrentReads benchmarks concurrent read operations.
// This tests connection pool efficiency and concurrent query handling.
func BenchmarkConcurrentReads(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Setup: create test data
	log := createBenchLog(b, "GB33BUKB20201555555555")
	err := tc.repo.Create(ctx, log)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := tc.repo.FindByID(ctx, log.LogID)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkConcurrentWrites benchmarks concurrent write operations.
// This tests connection pool efficiency and transaction handling under load.
func BenchmarkConcurrentWrites(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	counter := 0
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			counter++
			log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", counter))
			err := tc.repo.Create(ctx, log)
			if err != nil {
				b.Fatal(err)
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
	logs := createBenchLogs(b, 100)
	err := tc.repo.CreateBatch(ctx, logs)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		operation := i % 10

		switch {
		case operation < 6: // 60% reads
			idx := i % len(logs)
			_, err := tc.repo.FindByID(ctx, logs[idx].LogID)
			if err != nil {
				b.Fatal(err)
			}

		case operation < 9: // 30% creates
			log := createBenchLog(b, fmt.Sprintf("GB33BUKB2020155%08d", i+1000))
			err := tc.repo.Create(ctx, log)
			if err != nil {
				b.Fatal(err)
			}

		default: // 10% updates
			idx := i % len(logs)
			log, err := tc.repo.FindByID(ctx, logs[idx].LogID)
			if err != nil {
				b.Fatal(err)
			}

			if log.StatusTracking.CurrentStatus == domain.TransactionStatusPending {
				err = log.MarkPosted("Posted in benchmark", nil)
				if err != nil {
					b.Fatal(err)
				}
				err = tc.repo.Update(ctx, log)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}

// BenchmarkTransactionThroughput measures maximum transaction throughput.
// This benchmark focuses on measuring transactions per second for bulk imports.
func BenchmarkTransactionThroughput(b *testing.B) {
	tc := setupBenchContainer(b)
	ctx := context.Background()

	// Use a large batch size to simulate real bulk import
	batchSize := 1000
	logs := createBenchLogs(b, batchSize)

	b.ResetTimer()
	err := tc.repo.CreateBatch(ctx, logs)
	if err != nil {
		b.Fatal(err)
	}

	// Report transactions per second
	duration := b.Elapsed()
	txnPerSec := float64(batchSize) / duration.Seconds()
	b.ReportMetric(txnPerSec, "txn/sec")
}
