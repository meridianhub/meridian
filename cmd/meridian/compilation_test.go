package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCompilation validates that the unified binary package compiles cleanly and all
// wiring helper functions are present with correct signatures. This test is a canary
// that breaks if service constructor signatures, proto registrations, or shared
// infrastructure types change in a way that would break the unified binary.
//
// It does NOT start any servers or connect to databases — it only validates that the
// Go type system accepts the wiring code at compile time.
func TestCompilation(t *testing.T) {
	// If this test compiles and runs, the unified binary wiring is type-correct.
	// Verify that key wiring functions exist (they are exercised at compile time).
	assert.NotNil(t, wireParty)
	assert.NotNil(t, wireReferenceData)
	assert.NotNil(t, wireMarketInformation)
	assert.NotNil(t, wireTenant)
	assert.NotNil(t, wireInternalAccount)
	assert.NotNil(t, wireFinancialAccounting)
	assert.NotNil(t, wirePositionKeeping)
	assert.NotNil(t, wireForecasting)
	assert.NotNil(t, wireCurrentAccount)
	assert.NotNil(t, wirePaymentOrder)
	assert.NotNil(t, wireReconciliation)
	assert.NotNil(t, wireGateway)
	assert.NotNil(t, registerServices)
	assert.NotNil(t, setDevDefaults)
}
