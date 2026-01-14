// Package e2e provides end-to-end integration tests for the internal bank account service.
// These tests verify the full account lifecycle from creation through closure,
// including multi-tenant isolation, multi-asset support, and correspondent banking.
//
// Run these tests with:
//
//	go test -tags=integration ./services/internal-bank-account/e2e/...
package e2e
