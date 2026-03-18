// Package validation provides input validation utilities for client-side
// pre-validation before making RPC calls.
//
// Validators in this package mirror the proto validation rules defined in the
// API layer, allowing callers to surface errors early without a network round-trip.
// Domain-layer primitives may enforce stricter invariants beyond what is checked here.
package validation
