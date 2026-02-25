// Package domain contains the core business logic for internal accounts.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// InternalAccount represents an internal account aggregate root.
// This is an immutable value type - all modification methods return a new instance.
//
// Internal accounts are used for the bank's own operations:
// - CLEARING: For settling transactions between accounts
// - NOSTRO: Our account at another institution (requires counterparty)
// - VOSTRO: Another institution's account at our institution (requires counterparty)
// - HOLDING: Temporarily holding funds during processing
// - SUSPENSE: Transactions that cannot be immediately categorized
// - REVENUE: Tracking income and revenue streams
// - EXPENSE: Tracking operational expenses
type InternalAccount struct {
	id              uuid.UUID
	accountID       string // Business identifier like 'IBA-001'
	accountCode     string // Unique code like 'GBP_CLEARING'
	name            string // Display name
	accountType     AccountType
	clearingPurpose ClearingPurpose // Only meaningful for CLEARING accounts
	instrumentCode  string          // References Reference Data (e.g., "USD", "GBP")
	dimension       string          // From reference_data (e.g., "CURRENCY", "ENERGY")
	status          AccountStatus
	orgPartyID      *uuid.UUID            // Organization party ID for org-scoped accounts (nil = global)
	counterparty    *CounterpartyDetails  // Required for NOSTRO/VOSTRO
	attributes      map[string]string     // Metadata
	version         int64
	createdAt       time.Time
	updatedAt       time.Time

	// Product Directory fields (immutable after creation)
	productTypeCode    string // Product type code from AccountTypeRegistry
	productTypeVersion int    // Pinned version of the product type definition
}

// AccountOption is a functional option for configuring InternalAccount creation.
type AccountOption func(*accountOptions)

type accountOptions struct {
	orgPartyID *uuid.UUID
}

// WithOrgPartyID sets the organization party ID for org-scoped accounts.
func WithOrgPartyID(id uuid.UUID) AccountOption {
	return func(o *accountOptions) {
		o.orgPartyID = &id
	}
}

// NewInternalAccount creates a new InternalAccount with validated fields.
// Returns a value type (not pointer) following the immutability pattern.
//
// Initial state:
//   - Status: ACTIVE
//   - Version: 1
//   - Counterparty: nil (must be set via UpdateCounterparty for NOSTRO/VOSTRO before use)
//
// Validation:
//   - accountID, accountCode, name cannot be empty
//   - accountType must be valid
//   - clearingPurpose must be valid if provided
//   - clearingPurpose can only be non-UNSPECIFIED for CLEARING accounts
//   - org-scoped accounts (orgPartyID non-nil) cannot be CLEARING type
func NewInternalAccount(
	accountID, accountCode, name string,
	accountType AccountType,
	clearingPurpose ClearingPurpose,
	instrumentCode, dimension string,
	opts ...AccountOption,
) (InternalAccount, error) {
	if accountID == "" {
		return InternalAccount{}, ErrAccountIDRequired
	}
	if accountCode == "" {
		return InternalAccount{}, ErrAccountCodeRequired
	}
	if name == "" {
		return InternalAccount{}, ErrNameRequired
	}
	if !accountType.IsValid() {
		return InternalAccount{}, ErrInvalidAccountType
	}
	if !clearingPurpose.IsValid() {
		return InternalAccount{}, ErrInvalidClearingPurpose
	}
	// Non-CLEARING accounts must not have a specific clearing purpose
	if accountType != AccountTypeClearing && clearingPurpose != ClearingPurposeUnspecified {
		return InternalAccount{}, ErrClearingPurposeNotAllowed
	}
	// CLEARING accounts must have a specific clearing purpose (not UNSPECIFIED)
	if accountType == AccountTypeClearing && clearingPurpose == ClearingPurposeUnspecified {
		return InternalAccount{}, ErrClearingPurposeRequired
	}

	// Apply functional options
	var options accountOptions
	for _, opt := range opts {
		opt(&options)
	}

	// Org-scoped accounts cannot be CLEARING type
	if options.orgPartyID != nil && accountType == AccountTypeClearing {
		return InternalAccount{}, ErrOrgScopedClearingNotAllowed
	}

	now := time.Now()
	return InternalAccount{
		id:              uuid.New(),
		accountID:       accountID,
		accountCode:     accountCode,
		name:            name,
		accountType:     accountType,
		clearingPurpose: clearingPurpose,
		instrumentCode:  instrumentCode,
		dimension:       dimension,
		orgPartyID:      options.orgPartyID,
		status:          AccountStatusActive,
		counterparty:    nil,
		attributes:      nil,
		version:         1,
		createdAt:       now,
		updatedAt:       now,
	}, nil
}

// Suspend transitions the account to SUSPENDED status.
// Returns a new instance with updated status.
// Returns error if the transition is not valid.
func (a InternalAccount) Suspend(reason string) (InternalAccount, error) {
	if err := ValidateTransition(a.status, AccountStatusSuspended); err != nil {
		return a, err
	}
	return a.withStatusChange(AccountStatusSuspended, reason), nil
}

// Activate transitions the account to ACTIVE status.
// Returns a new instance with updated status.
// Returns error if the transition is not valid.
func (a InternalAccount) Activate() (InternalAccount, error) {
	if err := ValidateTransition(a.status, AccountStatusActive); err != nil {
		return a, err
	}
	return a.withStatusChange(AccountStatusActive, ""), nil
}

// Close transitions the account to CLOSED status.
// This is a terminal state - no further transitions are allowed.
// Returns a new instance with updated status.
// Returns error if the transition is not valid.
func (a InternalAccount) Close(reason string) (InternalAccount, error) {
	if err := ValidateTransition(a.status, AccountStatusClosed); err != nil {
		return a, err
	}
	return a.withStatusChange(AccountStatusClosed, reason), nil
}

// Rename updates the account display name.
// Returns a new instance with updated name.
// Returns error if account is closed or name is empty.
func (a InternalAccount) Rename(newName string) (InternalAccount, error) {
	if a.status == AccountStatusClosed {
		return a, ErrAccountClosed
	}
	if newName == "" {
		return a, ErrNameRequired
	}

	newAccount := a.copyWithUpdatedTime()
	newAccount.name = newName
	newAccount.version++
	return newAccount, nil
}

// UpdateCounterparty sets or updates the counterparty details.
// Returns a new instance with updated counterparty.
//
// Validation:
//   - NOSTRO/VOSTRO accounts REQUIRE counterparty details (cannot pass nil)
//   - Other account types REJECT counterparty details (cannot pass non-nil)
func (a InternalAccount) UpdateCounterparty(details *CounterpartyDetails) (InternalAccount, error) {
	if a.status == AccountStatusClosed {
		return a, ErrAccountClosed
	}

	requiresCounterparty := a.accountType.RequiresCorrespondent()

	if requiresCounterparty && details == nil {
		return a, ErrCounterpartyRequired
	}
	if !requiresCounterparty && details != nil {
		return a, ErrCounterpartyNotAllowed
	}

	// Create new instance with updated counterparty
	newAccount := a.copyWithUpdatedTime()
	newAccount.counterparty = details
	newAccount.version++
	return newAccount, nil
}

// withStatusChange creates a new instance with updated status.
// This is a private helper that handles the immutable update pattern.
func (a InternalAccount) withStatusChange(newStatus AccountStatus, _ string) InternalAccount {
	newAccount := a.copyWithUpdatedTime()
	newAccount.status = newStatus
	newAccount.version++
	return newAccount
}

// copyWithUpdatedTime creates a copy of the account with updated timestamp.
// Deep copies maps to ensure immutability.
func (a InternalAccount) copyWithUpdatedTime() InternalAccount {
	newAccount := a
	newAccount.updatedAt = time.Now()

	// Deep copy attributes map
	if a.attributes != nil {
		newAccount.attributes = make(map[string]string, len(a.attributes))
		for k, v := range a.attributes {
			newAccount.attributes[k] = v
		}
	}

	return newAccount
}

// Getters for all unexported fields.

// ID returns the internal unique identifier.
func (a InternalAccount) ID() uuid.UUID {
	return a.id
}

// AccountID returns the business identifier (e.g., 'IBA-001').
func (a InternalAccount) AccountID() string {
	return a.accountID
}

// AccountCode returns the unique code (e.g., 'GBP_CLEARING').
func (a InternalAccount) AccountCode() string {
	return a.accountCode
}

// Name returns the display name.
func (a InternalAccount) Name() string {
	return a.name
}

// AccountType returns the account type.
func (a InternalAccount) AccountType() AccountType {
	return a.accountType
}

// ClearingPurpose returns the clearing purpose.
// Only meaningful for CLEARING account type; returns UNSPECIFIED for other types.
func (a InternalAccount) ClearingPurpose() ClearingPurpose {
	return a.clearingPurpose
}

// InstrumentCode returns the instrument code reference (e.g., "USD", "GBP").
func (a InternalAccount) InstrumentCode() string {
	return a.instrumentCode
}

// Dimension returns the dimension from reference data (e.g., "CURRENCY", "ENERGY").
func (a InternalAccount) Dimension() string {
	return a.dimension
}

// Status returns the current account status.
func (a InternalAccount) Status() AccountStatus {
	return a.status
}

// OrgPartyID returns the organization party ID for org-scoped accounts.
// Returns nil for global accounts.
func (a InternalAccount) OrgPartyID() *uuid.UUID {
	return a.orgPartyID
}

// IsScopedToOrganization returns true if the account is scoped to a specific organization.
// Global accounts (orgPartyID == nil) return false.
func (a InternalAccount) IsScopedToOrganization() bool {
	return a.orgPartyID != nil
}

// Counterparty returns the counterparty details.
// Returns nil for non-NOSTRO/VOSTRO accounts.
func (a InternalAccount) Counterparty() *CounterpartyDetails {
	return a.counterparty
}

// Attributes returns a copy of the metadata attributes.
// Returns nil if no attributes are set.
func (a InternalAccount) Attributes() map[string]string {
	if a.attributes == nil {
		return nil
	}
	result := make(map[string]string, len(a.attributes))
	for k, v := range a.attributes {
		result[k] = v
	}
	return result
}

// ProductTypeCode returns the product type code from the Product Directory.
// Empty string means the account was created before product type migration.
func (a InternalAccount) ProductTypeCode() string {
	return a.productTypeCode
}

// ProductTypeVersion returns the pinned version of the product type definition.
// Zero means no version was pinned (use latest).
func (a InternalAccount) ProductTypeVersion() int {
	return a.productTypeVersion
}

// Version returns the version number for optimistic locking.
func (a InternalAccount) Version() int64 {
	return a.version
}

// CreatedAt returns the creation timestamp.
func (a InternalAccount) CreatedAt() time.Time {
	return a.createdAt
}

// UpdatedAt returns the last update timestamp.
func (a InternalAccount) UpdatedAt() time.Time {
	return a.updatedAt
}

// InternalAccountBuilder provides a builder pattern for reconstructing
// InternalAccount from persistence layer. This bypasses normal validation
// since we assume persisted data was already validated.
type InternalAccountBuilder struct {
	account InternalAccount
}

// NewInternalAccountBuilder creates a new builder for InternalAccount reconstruction.
func NewInternalAccountBuilder() *InternalAccountBuilder {
	return &InternalAccountBuilder{
		account: InternalAccount{},
	}
}

// WithID sets the internal unique identifier.
func (b *InternalAccountBuilder) WithID(id uuid.UUID) *InternalAccountBuilder {
	b.account.id = id
	return b
}

// WithAccountID sets the business identifier.
func (b *InternalAccountBuilder) WithAccountID(accountID string) *InternalAccountBuilder {
	b.account.accountID = accountID
	return b
}

// WithAccountCode sets the unique code.
func (b *InternalAccountBuilder) WithAccountCode(accountCode string) *InternalAccountBuilder {
	b.account.accountCode = accountCode
	return b
}

// WithName sets the display name.
func (b *InternalAccountBuilder) WithName(name string) *InternalAccountBuilder {
	b.account.name = name
	return b
}

// WithAccountType sets the account type.
func (b *InternalAccountBuilder) WithAccountType(accountType AccountType) *InternalAccountBuilder {
	b.account.accountType = accountType
	return b
}

// WithClearingPurpose sets the clearing purpose.
func (b *InternalAccountBuilder) WithClearingPurpose(clearingPurpose ClearingPurpose) *InternalAccountBuilder {
	b.account.clearingPurpose = clearingPurpose
	return b
}

// WithInstrumentCode sets the instrument code reference.
func (b *InternalAccountBuilder) WithInstrumentCode(instrumentCode string) *InternalAccountBuilder {
	b.account.instrumentCode = instrumentCode
	return b
}

// WithDimension sets the dimension.
func (b *InternalAccountBuilder) WithDimension(dimension string) *InternalAccountBuilder {
	b.account.dimension = dimension
	return b
}

// WithStatus sets the account status.
func (b *InternalAccountBuilder) WithStatus(status AccountStatus) *InternalAccountBuilder {
	b.account.status = status
	return b
}

// WithOrgPartyID sets the organization party ID for org-scoped accounts.
func (b *InternalAccountBuilder) WithOrgPartyID(orgPartyID *uuid.UUID) *InternalAccountBuilder {
	b.account.orgPartyID = orgPartyID
	return b
}

// WithCounterparty sets the counterparty details.
func (b *InternalAccountBuilder) WithCounterparty(counterparty *CounterpartyDetails) *InternalAccountBuilder {
	b.account.counterparty = counterparty
	return b
}

// WithAttributes sets the metadata attributes.
func (b *InternalAccountBuilder) WithAttributes(attributes map[string]string) *InternalAccountBuilder {
	if attributes != nil {
		b.account.attributes = make(map[string]string, len(attributes))
		for k, v := range attributes {
			b.account.attributes[k] = v
		}
	}
	return b
}

// WithProductTypeCode sets the product type code.
func (b *InternalAccountBuilder) WithProductTypeCode(code string) *InternalAccountBuilder {
	b.account.productTypeCode = code
	return b
}

// WithProductTypeVersion sets the product type version.
func (b *InternalAccountBuilder) WithProductTypeVersion(version int) *InternalAccountBuilder {
	b.account.productTypeVersion = version
	return b
}

// WithVersion sets the version number.
func (b *InternalAccountBuilder) WithVersion(version int64) *InternalAccountBuilder {
	b.account.version = version
	return b
}

// WithCreatedAt sets the creation timestamp.
func (b *InternalAccountBuilder) WithCreatedAt(createdAt time.Time) *InternalAccountBuilder {
	b.account.createdAt = createdAt
	return b
}

// WithUpdatedAt sets the last update timestamp.
func (b *InternalAccountBuilder) WithUpdatedAt(updatedAt time.Time) *InternalAccountBuilder {
	b.account.updatedAt = updatedAt
	return b
}

// Build returns the constructed InternalAccount.
// This is used for persistence reconstruction and does not validate.
func (b *InternalAccountBuilder) Build() InternalAccount {
	return b.account
}
