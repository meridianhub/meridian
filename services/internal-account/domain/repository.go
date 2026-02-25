// Package domain contains the core business logic for internal accounts.
package domain

import (
	"context"

	"github.com/google/uuid"
)

// Repository defines the persistence port for InternalAccount aggregates.
// This interface follows the hexagonal architecture pattern, allowing the domain
// to remain independent of specific persistence implementations.
//
// Implementations must be thread-safe and handle tenant context from ctx.
type Repository interface {
	// Save persists a new or updated account.
	// For new accounts, returns ErrDuplicateAccountCode if the code already exists.
	// For updates, returns ErrVersionMismatch on optimistic lock failure.
	Save(ctx context.Context, account InternalAccount) error

	// FindByID retrieves an account by its UUID.
	// Returns ErrAccountNotFound if the account does not exist.
	FindByID(ctx context.Context, id uuid.UUID) (InternalAccount, error)

	// FindByCode retrieves an account by its unique code.
	// Returns ErrAccountNotFound if the account does not exist.
	FindByCode(ctx context.Context, accountCode string) (InternalAccount, error)

	// List returns accounts matching the filter criteria.
	// Returns an empty slice if no accounts match the filter.
	List(ctx context.Context, filter ListFilter) ([]InternalAccount, error)

	// ExistsByCode checks if an account with the given code exists.
	ExistsByCode(ctx context.Context, accountCode string) (bool, error)
}

// ListFilter specifies criteria for listing accounts.
// Nil pointer fields are treated as "match all" for that criterion.
type ListFilter struct {
	// AccountType filters by account type. Nil matches all types.
	AccountType *AccountType

	// InstrumentCode filters by instrument code. Nil matches all instruments.
	InstrumentCode *string

	// Status filters by account status. Nil matches all statuses.
	Status *AccountStatus

	// ClearingPurpose filters by clearing purpose. Nil matches all purposes.
	// Only meaningful when filtering for CLEARING account types.
	ClearingPurpose *ClearingPurpose

	// OrgPartyID filters by organization party ID.
	// Nil matches all accounts (both global and org-scoped).
	OrgPartyID *uuid.UUID

	// Limit specifies the maximum number of results to return.
	// Zero or negative values use the implementation's default limit.
	Limit int

	// Offset specifies the number of results to skip for pagination.
	Offset int
}
