//go:build integration
// +build integration

// Package clearinge2e provides end-to-end integration tests for the complete
// clearing account flow across multiple services.
//
// These tests validate the full lifecycle of deposit, withdrawal, and payment
// settlement operations that involve:
//
//   - Current Account: Initiates customer-facing operations
//   - Internal Account: Resolves clearing accounts by purpose (deposit/withdrawal)
//   - Position Keeping: Records position logs for balance tracking
//   - Financial Accounting: Creates balanced ledger postings
//   - Payment Order: Orchestrates payment settlement flows
//
// The tests use Testcontainers to spin up isolated PostgreSQL instances for each
// service's bounded context, ensuring complete isolation and reproducibility.
//
// Test organization:
//   - infra_test.go: Test infrastructure setup (containers, databases, services)
//   - deposit_flow_test.go: Deposit clearing flow E2E tests
//   - withdrawal_flow_test.go: Withdrawal clearing flow E2E tests
//   - payment_settlement_test.go: Payment settlement E2E tests
//   - multi_asset_test.go: Multi-asset (GBP, KWH, GPU-HOUR) clearing tests
//   - error_handling_test.go: Resilience and fallback behavior tests
//
// Running the tests:
//
//	go test -tags integration ./tests/clearinge2e/...
//
// Or with verbose output:
//
//	go test -tags integration -v ./tests/clearinge2e/...
package clearinge2e
