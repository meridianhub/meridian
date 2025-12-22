//go:build integration
// +build integration

// Package audit_e2e provides end-to-end integration tests for the multi-service audit system.
//
// These tests validate the complete audit flow from service operations through to tenant
// audit_log tables, with per-service bounded context enforcement and multi-tenant isolation.
//
// Test execution requires Docker (for testcontainers) and should be run with integration tag:
//
//	go test -v -tags=integration ./tests/audit-e2e/... -timeout 10m
//
// The tests are designed to validate:
// - Multi-tenant writes across multiple service databases
// - Bounded context enforcement (database-per-service rule)
// - Independent failure handling (one service failure doesn't block others)
// - Audit trail completeness across services
// - Idempotent write behavior
package audit_e2e
