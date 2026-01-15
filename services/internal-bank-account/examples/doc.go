// Package examples contains runnable code examples demonstrating Internal Bank Account service usage.
//
// The example files in this directory are excluded from normal compilation (via //go:build ignore)
// because they each contain a main function. To run an example, use go run directly:
//
//	go run ./services/internal-bank-account/examples/create_clearing_account.go
//	go run ./services/internal-bank-account/examples/query_balance.go
//	go run ./services/internal-bank-account/examples/correspondent_account.go
//	go run ./services/internal-bank-account/examples/multi_asset.go
//	go run ./services/internal-bank-account/examples/account_lifecycle.go
//
// Each example demonstrates a specific aspect of the Internal Bank Account service:
//
//   - create_clearing_account.go: Basic account creation with tenant context
//   - query_balance.go: Balance retrieval via Position Keeping delegation
//   - correspondent_account.go: NOSTRO/VOSTRO setup for correspondent banking
//   - multi_asset.go: Creating energy, compute, and carbon accounts
//   - account_lifecycle.go: Full account lifecycle management
//
// Prerequisites:
//   - Internal Bank Account service running at localhost:50057
//   - Position Keeping service running at localhost:50053 (for balance queries)
//   - Valid tenant context (examples use "test-tenant")
package examples
