// Package testhelpers provides shared test utilities for position-keeping tests.
//
// For domain entity factories (FinancialPositionLog, Money, TransactionCapturedEvent),
// use the domain/testfixtures package which provides a comprehensive builder-pattern API.
//
// This package provides service-level test utilities that complement the domain fixtures.
package testhelpers

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// ContextWithTenant returns a context with the given tenant ID for position-keeping tests.
// This is a convenience wrapper around testdb.ContextWithTenant.
func ContextWithTenant(t *testing.T, tenantID string) context.Context {
	t.Helper()
	return testdb.ContextWithTenant(t, tenantID)
}

// DefaultTestTenantID is the standard tenant ID used in position-keeping tests.
const DefaultTestTenantID = "test_pk_tenant"
