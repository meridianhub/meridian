// Package clients provides shared client utilities including error definitions.
package clients

import "errors"

// Orchestrator configuration errors for nil dependency validation.
// These sentinel errors are returned by orchestrator constructors instead of panicking,
// allowing callers to handle initialization failures gracefully.
//
// When service startup fails due to these errors, the application will:
// 1. Exit with a non-zero status code
// 2. Log the specific error with context about which dependency is missing
// 3. Enter crash loop backoff in Kubernetes until the configuration is fixed
//
// DevOps remediation:
//   - ErrConfigLoggerNil: Check that logger initialization completed before service construction
//   - ErrConfigRepositoryNil: Verify database connection succeeded and repository was created
//   - ErrConfigClientNil: Check that the required gRPC client connection was established
//
// Example error message in logs:
//
//	"failed to create service: failed to create deposit orchestrator: config: logger cannot be nil"
//
// This indicates the logger dependency was nil when constructing the orchestrator.
var (
	// ErrConfigLoggerNil indicates the logger dependency is nil.
	// Remediation: Ensure logger is initialized before creating orchestrators.
	ErrConfigLoggerNil = errors.New("config: logger cannot be nil")

	// ErrConfigRepositoryNil indicates the repository dependency is nil.
	// Remediation: Verify database connection succeeded and repository was created.
	ErrConfigRepositoryNil = errors.New("config: repository cannot be nil")

	// ErrConfigClientNil indicates a required client dependency is nil.
	// Remediation: Check that the required gRPC client connection was established.
	// For service-specific clients, wrap this error with additional context.
	ErrConfigClientNil = errors.New("config: client cannot be nil")
)

// Service-specific client configuration errors.
// These provide service-specific context for nil dependency validation.
var (
	// ErrConfigPositionKeepingClientNil indicates the position keeping client is nil.
	ErrConfigPositionKeepingClientNil = errors.New("config: position keeping client cannot be nil")

	// ErrConfigFinancialAccountingClientNil indicates the financial accounting client is nil.
	ErrConfigFinancialAccountingClientNil = errors.New("config: financial accounting client cannot be nil")
)
