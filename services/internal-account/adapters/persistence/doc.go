// Package persistence provides PostgreSQL-based storage for internal accounts.
//
// This package implements the domain.Repository interface using GORM as the ORM layer,
// providing full CRUD operations with multi-tenant isolation and optimistic locking.
//
// # Multi-tenancy
//
// All repository operations are automatically scoped to the tenant from the gRPC
// context. The tenant ID is extracted from context metadata and used to filter
// queries, ensuring complete data isolation between organizations.
//
// # Optimistic Locking
//
// Updates use version-based concurrency control. Each account has a version field
// that increments on every update. If the expected version does not match the
// current version, the update fails with [ErrVersionConflict].
//
// # Transaction Support
//
// The repository supports explicit transaction management via [Repository.WithTx]:
//
//	tx := repo.DB().Begin()
//	defer tx.Rollback()
//
//	txRepo := repo.WithTx(tx)
//	// Use txRepo for all operations within this transaction
//
//	tx.Commit()
//
// # Key Types
//
//   - [Repository]: Implements domain.Repository for PostgreSQL
//   - [AccountEntity]: GORM entity for database persistence
//   - [ErrAccountNotFound]: Returned when account lookup fails
//   - [ErrDuplicateCode]: Returned when account code uniqueness is violated
//   - [ErrVersionConflict]: Returned when optimistic lock fails
//
// # Database Schema
//
// The repository operates on the 'internal_accounts' table managed by Atlas
// migrations in the migrations/ directory. The schema supports:
//
//   - UUID primary key
//   - Tenant-scoped unique constraint on account_code
//   - JSONB storage for counterparty details
//   - Timestamp tracking (created_at, updated_at)
//   - Version column for optimistic locking
//
// # Example Usage
//
// Creating a repository and saving an account:
//
//	repo := persistence.NewRepository(gormDB)
//
//	// Create a new account (domain package handles business validation)
//	account, _ := domain.NewInternalAccount(
//	    "CLR-001", "GBP_CLEARING", "GBP Clearing Pool",
//	    domain.AccountTypeClearing, "GBP", "CURRENCY",
//	)
//
//	// Persist (tenant ID extracted from context automatically)
//	err := repo.Create(ctx, account)
//	if errors.Is(err, persistence.ErrDuplicateCode) {
//	    // Handle duplicate account code
//	}
package persistence
