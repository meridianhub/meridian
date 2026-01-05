// Package shared provides core utilities for the position-tool commands.
//
// This package contains reusable infrastructure components shared between
// the `rebucket` and `import` subcommands, including:
//
//   - CEL evaluator wrapper for bucket key generation from measurement attributes
//   - Batched position insert manager with efficient COPY protocol support
//   - Audit logging integration for initial import operations
//   - Progress tracker with channel-based callbacks and dry-run mode
//   - Tenant isolation helper for multi-tenant schema scoping
//
// All components are designed for high-throughput bulk operations with
// proper error handling, progress reporting, and audit trail generation.
package infra
