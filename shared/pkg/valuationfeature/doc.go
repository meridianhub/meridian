// Package valuationfeature provides shared domain, persistence, and CRUD operations
// for valuation features across account services (Current Account, Internal Account).
//
// A [ValuationFeature] maps an input instrument (e.g., USD) to an account's native
// instrument (e.g., GBP) using a specific valuation method from the Valuation Engine
// Service. At most one ACTIVE feature is permitted per account per input instrument.
//
// State transitions are enforced via [Activate] and [Terminate] methods on the domain
// entity, which follow the INITIATED → ACTIVE → TERMINATED lifecycle (ADR-012).
package valuationfeature
