package service

import (
	"context"
)

// DiagnosticDetail holds drill-down information from Financial Accounting.
type DiagnosticDetail struct {
	AccountID       string
	InstrumentCode  string
	JournalEntryIDs []string
	Message         string
}

// FinancialAccountingClient defines the interface for querying Financial Accounting
// service for diagnostic drill-down on balance assertion failures.
type FinancialAccountingClient interface {
	// GetDiagnosticDetail retrieves detailed journal entry information
	// for a given account and instrument to help diagnose imbalances.
	GetDiagnosticDetail(ctx context.Context, accountID, instrumentCode string) (*DiagnosticDetail, error)
}
