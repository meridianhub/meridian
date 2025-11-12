package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/position-keeping/domain"
	"github.com/shopspring/decimal"
)

// BenchmarkNewFinancialPositionLog benchmarks creating a new financial position log.
func BenchmarkNewFinancialPositionLog(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	entry, _ := domain.NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		money,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Test transaction",
		"REF-001",
		domain.TransactionSourceManual,
	)
	lineage, _ := domain.NewTransactionLineage(
		uuid.New(),
		"test-transaction",
		nil,
		nil,
		nil,
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = domain.NewFinancialPositionLog("ACC-001", entry, lineage)
	}
}

// BenchmarkAddEntry benchmarks adding entries to a financial position log.
func BenchmarkAddEntry(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	initialEntry, _ := domain.NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		money,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Test transaction",
		"REF-001",
		domain.TransactionSourceManual,
	)
	lineage, _ := domain.NewTransactionLineage(
		uuid.New(),
		"test-transaction",
		nil,
		nil,
		nil,
	)
	log, _ := domain.NewFinancialPositionLog("ACC-001", initialEntry, lineage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entry, _ := domain.NewTransactionLogEntry(
			uuid.New(),
			"ACC-001",
			money,
			domain.PostingDirectionDebit,
			time.Now().UTC(),
			"Test transaction",
			"REF-001",
			domain.TransactionSourceManual,
		)
		_ = log.AddEntry(entry)
	}
}

// BenchmarkMarkReconciled benchmarks marking a log as reconciled.
func BenchmarkMarkReconciled(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
		entry, _ := domain.NewTransactionLogEntry(
			uuid.New(),
			"ACC-001",
			money,
			domain.PostingDirectionDebit,
			time.Now().UTC(),
			"Test transaction",
			"REF-001",
			domain.TransactionSourceManual,
		)
		lineage, _ := domain.NewTransactionLineage(
			uuid.New(),
			"test-transaction",
			nil,
			nil,
			nil,
		)
		log, _ := domain.NewFinancialPositionLog("ACC-001", entry, lineage)
		b.StartTimer()

		_ = log.MarkReconciled(domain.ReconciliationStatusMatched, "Test reconciliation", nil)
	}
}

// BenchmarkMarkPosted benchmarks marking a log as posted.
func BenchmarkMarkPosted(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
		entry, _ := domain.NewTransactionLogEntry(
			uuid.New(),
			"ACC-001",
			money,
			domain.PostingDirectionDebit,
			time.Now().UTC(),
			"Test transaction",
			"REF-001",
			domain.TransactionSourceManual,
		)
		lineage, _ := domain.NewTransactionLineage(
			uuid.New(),
			"test-transaction",
			nil,
			nil,
			nil,
		)
		log, _ := domain.NewFinancialPositionLog("ACC-001", entry, lineage)
		b.StartTimer()

		_ = log.MarkPosted("Test posting", nil)
	}
}

// BenchmarkStateTransitionChain benchmarks a complete state transition chain.
func BenchmarkStateTransitionChain(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
		entry, _ := domain.NewTransactionLogEntry(
			uuid.New(),
			"ACC-001",
			money,
			domain.PostingDirectionDebit,
			time.Now().UTC(),
			"Test transaction",
			"REF-001",
			domain.TransactionSourceManual,
		)
		lineage, _ := domain.NewTransactionLineage(
			uuid.New(),
			"test-transaction",
			nil,
			nil,
			nil,
		)
		log, _ := domain.NewFinancialPositionLog("ACC-001", entry, lineage)
		b.StartTimer()

		_ = log.MarkReconciled(domain.ReconciliationStatusMatched, "Reconciled", nil)
		_ = log.MarkPosted("Posted", nil)
	}
}

// BenchmarkAddAuditEntry benchmarks adding audit entries.
func BenchmarkAddAuditEntry(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	entry, _ := domain.NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		money,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Test transaction",
		"REF-001",
		domain.TransactionSourceManual,
	)
	lineage, _ := domain.NewTransactionLineage(
		uuid.New(),
		"test-transaction",
		nil,
		nil,
		nil,
	)
	log, _ := domain.NewFinancialPositionLog("ACC-001", entry, lineage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		auditEntry, _ := domain.NewAuditTrailEntry(
			"system",
			"transaction_updated",
			"Test audit entry",
			"127.0.0.1",
			map[string]string{"source": "benchmark"},
		)
		_ = log.AddAuditEntry(auditEntry)
	}
}

// BenchmarkEntryCount benchmarks counting entries in a log.
func BenchmarkEntryCount(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	entry, _ := domain.NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		money,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Test transaction",
		"REF-001",
		domain.TransactionSourceManual,
	)
	lineage, _ := domain.NewTransactionLineage(
		uuid.New(),
		"test-transaction",
		nil,
		nil,
		nil,
	)
	log, _ := domain.NewFinancialPositionLog("ACC-001", entry, lineage)

	// Add several entries
	for i := 0; i < 100; i++ {
		e, _ := domain.NewTransactionLogEntry(
			uuid.New(),
			"ACC-001",
			money,
			domain.PostingDirectionDebit,
			time.Now().UTC(),
			"Test transaction",
			"REF-001",
			domain.TransactionSourceManual,
		)
		_ = log.AddEntry(e)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = log.EntryCount()
	}
}

// BenchmarkStatusChecks benchmarks status checking methods.
func BenchmarkStatusChecks(b *testing.B) {
	money, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
	entry, _ := domain.NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		money,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Test transaction",
		"REF-001",
		domain.TransactionSourceManual,
	)
	lineage, _ := domain.NewTransactionLineage(
		uuid.New(),
		"test-transaction",
		nil,
		nil,
		nil,
	)
	log, _ := domain.NewFinancialPositionLog("ACC-001", entry, lineage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = log.IsPosted()
		_ = log.IsReconciled()
		_ = log.IsFinal()
	}
}
