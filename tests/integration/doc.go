// Package integration provides cross-service integration tests for the Meridian platform.
//
// NOTE: Most tests in this package are currently disabled (build tag: integration_broken)
// due to API changes in the current-account and position-keeping domains. They will be
// re-enabled once the domain APIs are stabilized.
//
// Test files:
//   - balance_ownership_test.go: Tests BIAN balance ownership between services
//   - stress_test.go: Performance and load testing
//   - error_handling_test.go: Error handling and circuit breaker tests
package integration
