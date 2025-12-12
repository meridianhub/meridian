package provisioner

import "errors"

// Test-specific sentinel errors for mock provisioner.
// Kept separate from public API to avoid polluting the exported error set.
var (
	// ErrTestDatabaseConnectionFailed simulates database connection failures in tests.
	ErrTestDatabaseConnectionFailed = errors.New("test: database connection failed")

	// ErrTestGeneric is a generic test error for failure simulation.
	ErrTestGeneric = errors.New("test: generic error")
)
