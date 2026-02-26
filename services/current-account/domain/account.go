// Package domain contains the core business logic for current accounts
package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Domain errors
var (
	ErrInsufficientFunds       = errors.New("insufficient funds")
	ErrAccountFrozen           = errors.New("account is frozen")
	ErrAccountClosed           = errors.New("account is closed")
	ErrInvalidAmount           = errors.New("invalid amount")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrInvalidFreezeReason     = errors.New("freeze reason must be at least 10 characters")
	ErrNonZeroBalance          = errors.New("account balance must be zero to close")
	ErrActiveLiens             = errors.New("account has active liens and cannot be closed")
	ErrOrgScopedWithoutParty   = errors.New("org-scoped account requires a party ID")
)

// AccountStatus represents the lifecycle state of an account
type AccountStatus string

// Account status constants
const (
	AccountStatusActive AccountStatus = "ACTIVE"
	AccountStatusFrozen AccountStatus = "FROZEN"
	AccountStatusClosed AccountStatus = "CLOSED"
)

// Valid state transitions for accounts:
//
//	ACTIVE ──► FROZEN (via Freeze) ──► CLOSED (via Close)
//	   ▲           │
//	   └───────────┘ (via Unfreeze)
//
// CLOSED is a terminal state - no transitions allowed from CLOSED.
// Direct ACTIVE → CLOSED is permitted for accounts with zero balance.

// StatusChange represents a recorded state transition for audit purposes.
// This prepares for the status_history JSONB column in persistence.
type StatusChange struct {
	From      AccountStatus
	To        AccountStatus
	Reason    string
	Timestamp time.Time
	ChangedBy string // User who initiated the status change (populated from persistence)
}

// CurrentAccount represents a BIAN current account facility domain model.
// This type is immutable: all fields are unexported and all methods return
// new instances rather than mutating the receiver.
type CurrentAccount struct {
	id                 uuid.UUID
	accountID          string
	externalIdentifier string // IBAN or other external identifier
	instrumentCode     string // Instrument code (e.g. GBP, kWh)
	dimension          string // Asset dimension (e.g. CURRENCY, ELECTRICITY)
	partyID            string
	orgPartyID         *uuid.UUID // NULL for personal accounts, set for org-scoped accounts
	balance            Money
	availableBalance   Money
	status             AccountStatus
	freezeReason       string         // Reason for freezing the account (required when frozen)
	statusHistory      []StatusChange // Audit trail of status changes
	balanceUpdatedAt   time.Time
	version            int64
	createdAt          time.Time
	updatedAt          time.Time
	productTypeCode    string // Immutable after creation - references Product Directory
	productTypeVersion int    // Immutable after creation - pinned version
	behaviorClass      string // Derived from product type at creation time, stored for query efficiency
}

// AccountOption is a functional option for configuring new account creation.
type AccountOption func(*CurrentAccount)

// WithOrgPartyID sets the organization party ID for org-scoped accounts.
func WithOrgPartyID(orgPartyID uuid.UUID) AccountOption {
	return func(a *CurrentAccount) {
		id := orgPartyID
		a.orgPartyID = &id
	}
}

// WithProductType sets the product type code and version (immutable after creation).
func WithProductType(code string, version int) AccountOption {
	return func(a *CurrentAccount) {
		a.productTypeCode = code
		a.productTypeVersion = version
	}
}

// WithBehaviorClass sets the behavior class derived from the product type at creation time.
// Stored for query efficiency - not re-derived on reads.
func WithBehaviorClass(behaviorClass string) AccountOption {
	return func(a *CurrentAccount) {
		a.behaviorClass = behaviorClass
	}
}

// NewCurrentAccount creates a new current account with the given parameters.
// Returns a value type (not pointer) following immutability principles.
// Use WithOrgPartyID option to create an org-scoped account.
//
// instrumentCode is the instrument code (currently currency codes, e.g. "GBP").
// Dimension defaults to "CURRENCY". Use NewCurrentAccountWithDimension for explicit dimensions.
func NewCurrentAccount(accountID, externalIdentifier, partyID, instrumentCode string, opts ...AccountOption) (CurrentAccount, error) {
	return NewCurrentAccountWithDimension(accountID, externalIdentifier, partyID, instrumentCode, "CURRENCY", opts...)
}

// NewCurrentAccountWithDimension creates a new current account with explicit instrument code and dimension.
//
// NOTE: This service currently only supports CURRENCY dimension (enforced by NewMoneyFromInstrument).
// Passing any other dimension returns ErrInvalidCurrency. The dimension field is stored on the domain
// model for future multi-asset support (task 1 adds the schema column; full non-currency support
// is deferred until the Money type is generalised beyond CURRENCY-only instruments).
func NewCurrentAccountWithDimension(accountID, externalIdentifier, partyID, instrumentCode, dimension string, opts ...AccountOption) (CurrentAccount, error) {
	now := time.Now()
	normalizedDimension := strings.ToUpper(dimension)
	zeroMoney, err := NewMoneyFromInstrument(instrumentCode, normalizedDimension, 0)
	if err != nil {
		return CurrentAccount{}, err
	}

	account := CurrentAccount{
		id:                 uuid.New(),
		accountID:          accountID,
		externalIdentifier: externalIdentifier,
		instrumentCode:     instrumentCode,
		dimension:          normalizedDimension,
		partyID:            partyID,
		balance:            zeroMoney,
		availableBalance:   zeroMoney,
		status:             AccountStatusActive,
		balanceUpdatedAt:   now,
		version:            1,
		createdAt:          now,
		updatedAt:          now,
	}

	for _, opt := range opts {
		opt(&account)
	}

	// Validation: org-scoped account requires a party ID
	if account.orgPartyID != nil && account.partyID == "" {
		return CurrentAccount{}, ErrOrgScopedWithoutParty
	}

	return account, nil
}

// Deposit adds funds to the account and returns a new account with the updated balance.
// The original account is not modified.
func (a CurrentAccount) Deposit(amount Money) (CurrentAccount, error) {
	if !amount.IsPositive() {
		return CurrentAccount{}, ErrInvalidAmount
	}

	if a.status == AccountStatusFrozen {
		return CurrentAccount{}, ErrAccountFrozen
	}

	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrAccountClosed
	}

	if amount.Currency() != a.balance.Currency() {
		return CurrentAccount{}, ErrCurrencyMismatch
	}

	// Use immutable Add method
	newBalance, err := a.balance.Add(amount)
	if err != nil {
		return CurrentAccount{}, err
	}

	now := time.Now()

	return CurrentAccount{
		id:                 a.id,
		accountID:          a.accountID,
		externalIdentifier: a.externalIdentifier,
		instrumentCode:     a.instrumentCode,
		dimension:          a.dimension,
		partyID:            a.partyID,
		orgPartyID:         a.orgPartyID,
		balance:            newBalance,
		availableBalance:   newBalance,
		status:             a.status,
		freezeReason:       a.freezeReason,
		statusHistory:      a.statusHistory,
		balanceUpdatedAt:   now,
		version:            a.version + 1,
		createdAt:          a.createdAt,
		updatedAt:          now,
		productTypeCode:    a.productTypeCode,
		productTypeVersion: a.productTypeVersion,
		behaviorClass:      a.behaviorClass,
	}, nil
}

// PrepareForCredit validates the account can receive a credit transaction and increments the
// version for optimistic locking. This method does NOT mutate balance locally - balance is
// managed externally by the Position Keeping service.
//
// Use this method when recording CREDIT transactions in Position Keeping while keeping
// optimistic locking protection against concurrent modifications.
func (a CurrentAccount) PrepareForCredit() (CurrentAccount, error) {
	if a.status == AccountStatusFrozen {
		return CurrentAccount{}, ErrAccountFrozen
	}

	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrAccountClosed
	}

	now := time.Now()
	return CurrentAccount{
		id:                 a.id,
		accountID:          a.accountID,
		externalIdentifier: a.externalIdentifier,
		instrumentCode:     a.instrumentCode,
		dimension:          a.dimension,
		partyID:            a.partyID,
		orgPartyID:         a.orgPartyID,
		balance:            a.balance, // Balance NOT modified - Position Keeping is source of truth
		availableBalance:   a.availableBalance,
		status:             a.status,
		freezeReason:       a.freezeReason,
		statusHistory:      a.statusHistory,
		balanceUpdatedAt:   a.balanceUpdatedAt,
		version:            a.version + 1, // Version incremented for optimistic locking
		createdAt:          a.createdAt,
		updatedAt:          now,
		productTypeCode:    a.productTypeCode,
		productTypeVersion: a.productTypeVersion,
		behaviorClass:      a.behaviorClass,
	}, nil
}

// PrepareForDebit validates the account can process a debit transaction (withdrawal) and
// increments the version for optimistic locking. This method validates sufficient funds
// but does NOT mutate balance locally - balance is managed externally by the Position
// Keeping service.
//
// Use this method when recording DEBIT transactions in Position Keeping while keeping
// optimistic locking protection against concurrent modifications.
func (a CurrentAccount) PrepareForDebit(amount Money) (CurrentAccount, error) {
	if !amount.IsPositive() {
		return CurrentAccount{}, ErrInvalidAmount
	}

	if a.status == AccountStatusFrozen {
		return CurrentAccount{}, ErrAccountFrozen
	}

	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrAccountClosed
	}

	if amount.Currency() != a.balance.Currency() {
		return CurrentAccount{}, ErrCurrencyMismatch
	}

	// Check if sufficient funds (via availableBalance).
	// Currency match is already verified above, so Compare cannot return an error here.
	cmp, err := amount.Compare(a.availableBalance)
	if err != nil {
		return CurrentAccount{}, err
	}
	if cmp > 0 {
		return CurrentAccount{}, ErrInsufficientFunds
	}

	now := time.Now()
	return CurrentAccount{
		id:                 a.id,
		accountID:          a.accountID,
		externalIdentifier: a.externalIdentifier,
		instrumentCode:     a.instrumentCode,
		dimension:          a.dimension,
		partyID:            a.partyID,
		orgPartyID:         a.orgPartyID,
		balance:            a.balance, // Balance NOT modified - Position Keeping is source of truth
		availableBalance:   a.availableBalance,
		status:             a.status,
		freezeReason:       a.freezeReason,
		statusHistory:      a.statusHistory,
		balanceUpdatedAt:   a.balanceUpdatedAt,
		version:            a.version + 1, // Version incremented for optimistic locking
		createdAt:          a.createdAt,
		updatedAt:          now,
		productTypeCode:    a.productTypeCode,
		productTypeVersion: a.productTypeVersion,
		behaviorClass:      a.behaviorClass,
	}, nil
}

// Withdraw removes funds from the account and returns a new account with the updated balance.
// The original account is not modified.
func (a CurrentAccount) Withdraw(amount Money) (CurrentAccount, error) {
	if !amount.IsPositive() {
		return CurrentAccount{}, ErrInvalidAmount
	}

	if a.status == AccountStatusFrozen {
		return CurrentAccount{}, ErrAccountFrozen
	}

	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrAccountClosed
	}

	if amount.Currency() != a.balance.Currency() {
		return CurrentAccount{}, ErrCurrencyMismatch
	}

	// Check if sufficient funds.
	// Currency match is already verified above, so Compare cannot return an error here.
	cmp, err := amount.Compare(a.availableBalance)
	if err != nil {
		return CurrentAccount{}, err
	}
	if cmp > 0 {
		return CurrentAccount{}, ErrInsufficientFunds
	}

	// Use immutable Subtract method
	newBalance, err := a.balance.Subtract(amount)
	if err != nil {
		return CurrentAccount{}, err
	}

	now := time.Now()

	return CurrentAccount{
		id:                 a.id,
		accountID:          a.accountID,
		externalIdentifier: a.externalIdentifier,
		instrumentCode:     a.instrumentCode,
		dimension:          a.dimension,
		partyID:            a.partyID,
		orgPartyID:         a.orgPartyID,
		balance:            newBalance,
		availableBalance:   newBalance,
		status:             a.status,
		freezeReason:       a.freezeReason,
		statusHistory:      a.statusHistory,
		balanceUpdatedAt:   now,
		version:            a.version + 1,
		createdAt:          a.createdAt,
		updatedAt:          now,
		productTypeCode:    a.productTypeCode,
		productTypeVersion: a.productTypeVersion,
		behaviorClass:      a.behaviorClass,
	}, nil
}

// withStatusChange creates a new CurrentAccount with the status changed and history recorded.
// Note: Uses time.Now() directly for simplicity. For precise test control, consider injecting
// a clock interface in a future refactor. ChangedBy is populated by the persistence layer
// from the request context, not here - domain operations don't have access to user identity.
func (a CurrentAccount) withStatusChange(newStatus AccountStatus, reason string) CurrentAccount {
	now := time.Now()

	// Record the status change for audit trail
	change := StatusChange{
		From:      a.status,
		To:        newStatus,
		Reason:    reason,
		Timestamp: now,
	}

	// Create a new slice to preserve immutability
	newHistory := make([]StatusChange, len(a.statusHistory), len(a.statusHistory)+1)
	copy(newHistory, a.statusHistory)
	newHistory = append(newHistory, change)

	// Determine freeze reason - keep existing if unfreezing, set new if freezing
	freezeReason := a.freezeReason
	switch newStatus {
	case AccountStatusFrozen:
		freezeReason = reason
	case AccountStatusActive:
		freezeReason = "" // Clear freeze reason when unfreezing
	case AccountStatusClosed:
		// Keep existing freeze reason for audit trail when closing
	}

	return CurrentAccount{
		id:                 a.id,
		accountID:          a.accountID,
		externalIdentifier: a.externalIdentifier,
		instrumentCode:     a.instrumentCode,
		dimension:          a.dimension,
		partyID:            a.partyID,
		orgPartyID:         a.orgPartyID,
		balance:            a.balance,
		availableBalance:   a.availableBalance,
		status:             newStatus,
		freezeReason:       freezeReason,
		statusHistory:      newHistory,
		balanceUpdatedAt:   a.balanceUpdatedAt,
		version:            a.version + 1,
		createdAt:          a.createdAt,
		updatedAt:          now,
		productTypeCode:    a.productTypeCode,
		productTypeVersion: a.productTypeVersion,
		behaviorClass:      a.behaviorClass,
	}
}

// Freeze suspends the account and returns a new account with frozen status.
// Requires a reason of at least 10 characters for audit purposes.
// Only valid from ACTIVE status. The original account is not modified.
func (a CurrentAccount) Freeze(reason string) (CurrentAccount, error) {
	if a.status != AccountStatusActive {
		return CurrentAccount{}, ErrInvalidStatusTransition
	}

	if len(reason) < 10 {
		return CurrentAccount{}, ErrInvalidFreezeReason
	}

	return a.withStatusChange(AccountStatusFrozen, reason), nil
}

// Unfreeze restores the account to active status and returns a new account.
// Only valid from FROZEN status. The original account is not modified.
func (a CurrentAccount) Unfreeze() (CurrentAccount, error) {
	if a.status != AccountStatusFrozen {
		return CurrentAccount{}, ErrInvalidStatusTransition
	}

	return a.withStatusChange(AccountStatusActive, "Account unfrozen"), nil
}

// Activate restores the account to active status and returns a new account.
// This method is kept for backward compatibility but delegates to Unfreeze for FROZEN accounts.
// The original account is not modified.
//
// Deprecated: Use Unfreeze() instead for transitioning from FROZEN to ACTIVE.
// NOTE: Migrate callers to Unfreeze() before removing this method.
func (a CurrentAccount) Activate() (CurrentAccount, error) {
	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrInvalidStatusTransition
	}

	if a.status == AccountStatusFrozen {
		return a.Unfreeze()
	}

	// Already active - no change needed
	return a, nil
}

// Close permanently closes the account and returns a new account with closed status.
// CLOSED is a terminal state - no further transitions are allowed.
//
// The reason parameter is recorded in the status history for audit purposes.
// If empty, a default reason of "Account closed" is used.
//
// Prerequisites (validated by this method):
//   - Account balance must be zero
//   - Account must not already be closed
//
// Prerequisites (must be validated by service layer - see ErrActiveLiens):
//   - Account must have no active liens (requires LienRepository check)
//
// The original account is not modified.
func (a CurrentAccount) Close(reason string) (CurrentAccount, error) {
	if a.status == AccountStatusClosed {
		return CurrentAccount{}, ErrInvalidStatusTransition
	}

	// Validate balance is zero
	if !a.balance.IsZero() {
		return CurrentAccount{}, ErrNonZeroBalance
	}

	closeReason := reason
	if closeReason == "" {
		closeReason = "Account closed"
	}
	return a.withStatusChange(AccountStatusClosed, closeReason), nil
}

// Accessor methods for unexported fields.
// These return copies of values, preserving immutability.

// ID returns the internal UUID of the account.
func (a CurrentAccount) ID() uuid.UUID { return a.id }

// AccountID returns the business account identifier (e.g., "ACC-xxx").
func (a CurrentAccount) AccountID() string { return a.accountID }

// ExternalIdentifier returns the external identifier (e.g., IBAN).
func (a CurrentAccount) ExternalIdentifier() string { return a.externalIdentifier }

// AccountIdentification returns the external identifier (IBAN).
//
// Deprecated: Use ExternalIdentifier() instead.
// NOTE: Migrate callers to ExternalIdentifier() before removing this method.
func (a CurrentAccount) AccountIdentification() string { return a.externalIdentifier }

// InstrumentCode returns the instrument code (currently currency codes, e.g. "GBP").
func (a CurrentAccount) InstrumentCode() string { return a.instrumentCode }

// Dimension returns the asset dimension (currently "CURRENCY").
func (a CurrentAccount) Dimension() string { return a.dimension }

// PartyID returns the party (customer) identifier.
func (a CurrentAccount) PartyID() string { return a.partyID }

// OrgPartyID returns the organization party ID, or nil for personal accounts.
func (a CurrentAccount) OrgPartyID() *uuid.UUID { return a.orgPartyID }

// IsScopedToOrganization returns true if this account is scoped to an organization.
func (a CurrentAccount) IsScopedToOrganization() bool { return a.orgPartyID != nil }

// Balance returns the current balance.
func (a CurrentAccount) Balance() Money { return a.balance }

// AvailableBalance returns the available balance.
func (a CurrentAccount) AvailableBalance() Money { return a.availableBalance }

// Status returns the account status.
func (a CurrentAccount) Status() AccountStatus { return a.status }

// FreezeReason returns the reason for freezing the account.
// Returns empty string if account is not frozen.
func (a CurrentAccount) FreezeReason() string { return a.freezeReason }

// StatusHistory returns a copy of the status change history for audit purposes.
// Returns nil if no status changes have occurred.
func (a CurrentAccount) StatusHistory() []StatusChange {
	if a.statusHistory == nil {
		return nil
	}
	// Return a copy to preserve immutability
	result := make([]StatusChange, len(a.statusHistory))
	copy(result, a.statusHistory)
	return result
}

// BalanceUpdatedAt returns when the balance was last updated.
func (a CurrentAccount) BalanceUpdatedAt() time.Time { return a.balanceUpdatedAt }

// Version returns the optimistic locking version.
func (a CurrentAccount) Version() int64 { return a.version }

// CreatedAt returns when the account was created.
func (a CurrentAccount) CreatedAt() time.Time { return a.createdAt }

// UpdatedAt returns when the account was last updated.
func (a CurrentAccount) UpdatedAt() time.Time { return a.updatedAt }

// ProductTypeCode returns the product type code from the Product Directory.
func (a CurrentAccount) ProductTypeCode() string { return a.productTypeCode }

// ProductTypeVersion returns the pinned product type version.
func (a CurrentAccount) ProductTypeVersion() int { return a.productTypeVersion }

// BehaviorClass returns the behavior class derived from the product type at creation time.
// Returns empty string for legacy accounts created before Product Directory integration.
func (a CurrentAccount) BehaviorClass() string { return a.behaviorClass }

// Builder pattern for reconstructing accounts from persistence layer.
// This is needed because the persistence layer needs to set all fields
// when loading from the database.

// CurrentAccountBuilder provides a fluent API for constructing CurrentAccount instances.
// This is primarily used by the persistence layer to reconstruct accounts from database rows.
type CurrentAccountBuilder struct {
	account CurrentAccount
}

// NewCurrentAccountBuilder creates a new builder instance.
func NewCurrentAccountBuilder() *CurrentAccountBuilder {
	return &CurrentAccountBuilder{}
}

// WithID sets the account UUID.
func (b *CurrentAccountBuilder) WithID(id uuid.UUID) *CurrentAccountBuilder {
	b.account.id = id
	return b
}

// WithAccountID sets the business account identifier.
func (b *CurrentAccountBuilder) WithAccountID(accountID string) *CurrentAccountBuilder {
	b.account.accountID = accountID
	return b
}

// WithExternalIdentifier sets the external identifier (e.g., IBAN).
func (b *CurrentAccountBuilder) WithExternalIdentifier(externalIdentifier string) *CurrentAccountBuilder {
	b.account.externalIdentifier = externalIdentifier
	return b
}

// WithAccountIdentification sets the external identifier (IBAN).
//
// Deprecated: Use WithExternalIdentifier() instead.
// NOTE: Migrate callers to WithExternalIdentifier() before removing this method.
func (b *CurrentAccountBuilder) WithAccountIdentification(iban string) *CurrentAccountBuilder {
	b.account.externalIdentifier = iban
	return b
}

// WithInstrumentCode sets the instrument code (e.g. "GBP", "kWh").
func (b *CurrentAccountBuilder) WithInstrumentCode(instrumentCode string) *CurrentAccountBuilder {
	b.account.instrumentCode = instrumentCode
	return b
}

// WithDimension sets the asset dimension (currently "CURRENCY" is supported).
// The value is normalized to uppercase.
func (b *CurrentAccountBuilder) WithDimension(dimension string) *CurrentAccountBuilder {
	b.account.dimension = strings.ToUpper(dimension)
	return b
}

// WithPartyID sets the party identifier.
func (b *CurrentAccountBuilder) WithPartyID(partyID string) *CurrentAccountBuilder {
	b.account.partyID = partyID
	return b
}

// WithOrgPartyID sets the organization party ID. Pass nil for personal accounts.
func (b *CurrentAccountBuilder) WithOrgPartyID(orgPartyID *uuid.UUID) *CurrentAccountBuilder {
	b.account.orgPartyID = orgPartyID
	return b
}

// WithBalance sets the balance.
func (b *CurrentAccountBuilder) WithBalance(balance Money) *CurrentAccountBuilder {
	b.account.balance = balance
	return b
}

// WithAvailableBalance sets the available balance.
func (b *CurrentAccountBuilder) WithAvailableBalance(availableBalance Money) *CurrentAccountBuilder {
	b.account.availableBalance = availableBalance
	return b
}

// WithStatus sets the account status.
func (b *CurrentAccountBuilder) WithStatus(status AccountStatus) *CurrentAccountBuilder {
	b.account.status = status
	return b
}

// WithFreezeReason sets the freeze reason.
func (b *CurrentAccountBuilder) WithFreezeReason(reason string) *CurrentAccountBuilder {
	b.account.freezeReason = reason
	return b
}

// WithStatusHistory sets the status change history.
func (b *CurrentAccountBuilder) WithStatusHistory(history []StatusChange) *CurrentAccountBuilder {
	b.account.statusHistory = history
	return b
}

// WithBalanceUpdatedAt sets when the balance was last updated.
func (b *CurrentAccountBuilder) WithBalanceUpdatedAt(t time.Time) *CurrentAccountBuilder {
	b.account.balanceUpdatedAt = t
	return b
}

// WithVersion sets the optimistic locking version.
func (b *CurrentAccountBuilder) WithVersion(version int64) *CurrentAccountBuilder {
	b.account.version = version
	return b
}

// WithCreatedAt sets when the account was created.
func (b *CurrentAccountBuilder) WithCreatedAt(t time.Time) *CurrentAccountBuilder {
	b.account.createdAt = t
	return b
}

// WithUpdatedAt sets when the account was last updated.
func (b *CurrentAccountBuilder) WithUpdatedAt(t time.Time) *CurrentAccountBuilder {
	b.account.updatedAt = t
	return b
}

// WithProductTypeCode sets the product type code.
func (b *CurrentAccountBuilder) WithProductTypeCode(code string) *CurrentAccountBuilder {
	b.account.productTypeCode = code
	return b
}

// WithProductTypeVersion sets the product type version.
func (b *CurrentAccountBuilder) WithProductTypeVersion(version int) *CurrentAccountBuilder {
	b.account.productTypeVersion = version
	return b
}

// WithBehaviorClass sets the behavior class derived from the product type at creation time.
func (b *CurrentAccountBuilder) WithBehaviorClass(behaviorClass string) *CurrentAccountBuilder {
	b.account.behaviorClass = behaviorClass
	return b
}

// Build returns the constructed CurrentAccount.
func (b *CurrentAccountBuilder) Build() CurrentAccount {
	return b.account
}
