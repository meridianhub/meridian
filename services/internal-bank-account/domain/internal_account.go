// Package domain contains the core business logic for internal bank accounts.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// InternalBankAccount represents an internal bank account aggregate root.
// This is an immutable value type - all modification methods return a new instance.
//
// Internal bank accounts are used for the bank's own operations:
// - CLEARING: For settling transactions between accounts
// - NOSTRO: Our account at another bank (requires correspondent)
// - VOSTRO: Another bank's account at our bank (requires correspondent)
// - HOLDING: Temporarily holding funds during processing
// - SUSPENSE: Transactions that cannot be immediately categorized
// - REVENUE: Tracking income and revenue streams
// - EXPENSE: Tracking operational expenses
type InternalBankAccount struct {
	id              uuid.UUID
	accountID       string // Business identifier like 'IBA-001'
	accountCode     string // Unique code like 'GBP_CLEARING'
	name            string // Display name
	accountType     AccountType
	clearingPurpose ClearingPurpose // Only meaningful for CLEARING accounts
	instrumentCode  string          // References Reference Data (e.g., "USD", "GBP")
	dimension       string          // From reference_data (e.g., "CURRENCY", "ENERGY")
	status          AccountStatus
	correspondent   *CorrespondentDetails // Required for NOSTRO/VOSTRO
	attributes      map[string]string     // Metadata
	version         int64
	createdAt       time.Time
	updatedAt       time.Time
}

// NewInternalBankAccount creates a new InternalBankAccount with validated fields.
// Returns a value type (not pointer) following the immutability pattern.
//
// Initial state:
//   - Status: ACTIVE
//   - Version: 1
//   - Correspondent: nil (must be set via UpdateCorrespondent for NOSTRO/VOSTRO before use)
//
// Validation:
//   - accountID, accountCode, name cannot be empty
//   - accountType must be valid
//   - clearingPurpose must be valid if provided
//   - clearingPurpose can only be non-UNSPECIFIED for CLEARING accounts
func NewInternalBankAccount(
	accountID, accountCode, name string,
	accountType AccountType,
	clearingPurpose ClearingPurpose,
	instrumentCode, dimension string,
) (InternalBankAccount, error) {
	if accountID == "" {
		return InternalBankAccount{}, ErrAccountIDRequired
	}
	if accountCode == "" {
		return InternalBankAccount{}, ErrAccountCodeRequired
	}
	if name == "" {
		return InternalBankAccount{}, ErrNameRequired
	}
	if !accountType.IsValid() {
		return InternalBankAccount{}, ErrInvalidAccountType
	}
	if !clearingPurpose.IsValid() {
		return InternalBankAccount{}, ErrInvalidClearingPurpose
	}
	// Non-CLEARING accounts must not have a specific clearing purpose
	if accountType != AccountTypeClearing && clearingPurpose != ClearingPurposeUnspecified {
		return InternalBankAccount{}, ErrClearingPurposeNotAllowed
	}
	// CLEARING accounts must have a specific clearing purpose (not UNSPECIFIED)
	if accountType == AccountTypeClearing && clearingPurpose == ClearingPurposeUnspecified {
		return InternalBankAccount{}, ErrClearingPurposeRequired
	}

	now := time.Now()
	return InternalBankAccount{
		id:              uuid.New(),
		accountID:       accountID,
		accountCode:     accountCode,
		name:            name,
		accountType:     accountType,
		clearingPurpose: clearingPurpose,
		instrumentCode:  instrumentCode,
		dimension:       dimension,
		status:          AccountStatusActive,
		correspondent:   nil,
		attributes:      nil,
		version:         1,
		createdAt:       now,
		updatedAt:       now,
	}, nil
}

// Suspend transitions the account to SUSPENDED status.
// Returns a new instance with updated status.
// Returns error if the transition is not valid.
func (a InternalBankAccount) Suspend(reason string) (InternalBankAccount, error) {
	if err := ValidateTransition(a.status, AccountStatusSuspended); err != nil {
		return a, err
	}
	return a.withStatusChange(AccountStatusSuspended, reason), nil
}

// Activate transitions the account to ACTIVE status.
// Returns a new instance with updated status.
// Returns error if the transition is not valid.
func (a InternalBankAccount) Activate() (InternalBankAccount, error) {
	if err := ValidateTransition(a.status, AccountStatusActive); err != nil {
		return a, err
	}
	return a.withStatusChange(AccountStatusActive, ""), nil
}

// Close transitions the account to CLOSED status.
// This is a terminal state - no further transitions are allowed.
// Returns a new instance with updated status.
// Returns error if the transition is not valid.
func (a InternalBankAccount) Close(reason string) (InternalBankAccount, error) {
	if err := ValidateTransition(a.status, AccountStatusClosed); err != nil {
		return a, err
	}
	return a.withStatusChange(AccountStatusClosed, reason), nil
}

// Rename updates the account display name.
// Returns a new instance with updated name.
// Returns error if account is closed or name is empty.
func (a InternalBankAccount) Rename(newName string) (InternalBankAccount, error) {
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

// UpdateCorrespondent sets or updates the correspondent bank details.
// Returns a new instance with updated correspondent.
//
// Validation:
//   - NOSTRO/VOSTRO accounts REQUIRE correspondent details (cannot pass nil)
//   - Other account types REJECT correspondent details (cannot pass non-nil)
func (a InternalBankAccount) UpdateCorrespondent(details *CorrespondentDetails) (InternalBankAccount, error) {
	if a.status == AccountStatusClosed {
		return a, ErrAccountClosed
	}

	requiresCorrespondent := a.accountType.RequiresCorrespondent()

	if requiresCorrespondent && details == nil {
		return a, ErrCorrespondentRequired
	}
	if !requiresCorrespondent && details != nil {
		return a, ErrCorrespondentNotAllowed
	}

	// Create new instance with updated correspondent
	newAccount := a.copyWithUpdatedTime()
	newAccount.correspondent = details
	newAccount.version++
	return newAccount, nil
}

// withStatusChange creates a new instance with updated status.
// This is a private helper that handles the immutable update pattern.
func (a InternalBankAccount) withStatusChange(newStatus AccountStatus, _ string) InternalBankAccount {
	newAccount := a.copyWithUpdatedTime()
	newAccount.status = newStatus
	newAccount.version++
	return newAccount
}

// copyWithUpdatedTime creates a copy of the account with updated timestamp.
// Deep copies maps to ensure immutability.
func (a InternalBankAccount) copyWithUpdatedTime() InternalBankAccount {
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
func (a InternalBankAccount) ID() uuid.UUID {
	return a.id
}

// AccountID returns the business identifier (e.g., 'IBA-001').
func (a InternalBankAccount) AccountID() string {
	return a.accountID
}

// AccountCode returns the unique code (e.g., 'GBP_CLEARING').
func (a InternalBankAccount) AccountCode() string {
	return a.accountCode
}

// Name returns the display name.
func (a InternalBankAccount) Name() string {
	return a.name
}

// AccountType returns the account type.
func (a InternalBankAccount) AccountType() AccountType {
	return a.accountType
}

// ClearingPurpose returns the clearing purpose.
// Only meaningful for CLEARING account type; returns UNSPECIFIED for other types.
func (a InternalBankAccount) ClearingPurpose() ClearingPurpose {
	return a.clearingPurpose
}

// InstrumentCode returns the instrument code reference (e.g., "USD", "GBP").
func (a InternalBankAccount) InstrumentCode() string {
	return a.instrumentCode
}

// Dimension returns the dimension from reference data (e.g., "CURRENCY", "ENERGY").
func (a InternalBankAccount) Dimension() string {
	return a.dimension
}

// Status returns the current account status.
func (a InternalBankAccount) Status() AccountStatus {
	return a.status
}

// Correspondent returns the correspondent bank details.
// Returns nil for non-NOSTRO/VOSTRO accounts.
func (a InternalBankAccount) Correspondent() *CorrespondentDetails {
	return a.correspondent
}

// Attributes returns a copy of the metadata attributes.
// Returns nil if no attributes are set.
func (a InternalBankAccount) Attributes() map[string]string {
	if a.attributes == nil {
		return nil
	}
	result := make(map[string]string, len(a.attributes))
	for k, v := range a.attributes {
		result[k] = v
	}
	return result
}

// Version returns the version number for optimistic locking.
func (a InternalBankAccount) Version() int64 {
	return a.version
}

// CreatedAt returns the creation timestamp.
func (a InternalBankAccount) CreatedAt() time.Time {
	return a.createdAt
}

// UpdatedAt returns the last update timestamp.
func (a InternalBankAccount) UpdatedAt() time.Time {
	return a.updatedAt
}

// InternalBankAccountBuilder provides a builder pattern for reconstructing
// InternalBankAccount from persistence layer. This bypasses normal validation
// since we assume persisted data was already validated.
type InternalBankAccountBuilder struct {
	account InternalBankAccount
}

// NewInternalBankAccountBuilder creates a new builder for InternalBankAccount reconstruction.
func NewInternalBankAccountBuilder() *InternalBankAccountBuilder {
	return &InternalBankAccountBuilder{
		account: InternalBankAccount{},
	}
}

// WithID sets the internal unique identifier.
func (b *InternalBankAccountBuilder) WithID(id uuid.UUID) *InternalBankAccountBuilder {
	b.account.id = id
	return b
}

// WithAccountID sets the business identifier.
func (b *InternalBankAccountBuilder) WithAccountID(accountID string) *InternalBankAccountBuilder {
	b.account.accountID = accountID
	return b
}

// WithAccountCode sets the unique code.
func (b *InternalBankAccountBuilder) WithAccountCode(accountCode string) *InternalBankAccountBuilder {
	b.account.accountCode = accountCode
	return b
}

// WithName sets the display name.
func (b *InternalBankAccountBuilder) WithName(name string) *InternalBankAccountBuilder {
	b.account.name = name
	return b
}

// WithAccountType sets the account type.
func (b *InternalBankAccountBuilder) WithAccountType(accountType AccountType) *InternalBankAccountBuilder {
	b.account.accountType = accountType
	return b
}

// WithClearingPurpose sets the clearing purpose.
func (b *InternalBankAccountBuilder) WithClearingPurpose(clearingPurpose ClearingPurpose) *InternalBankAccountBuilder {
	b.account.clearingPurpose = clearingPurpose
	return b
}

// WithInstrumentCode sets the instrument code reference.
func (b *InternalBankAccountBuilder) WithInstrumentCode(instrumentCode string) *InternalBankAccountBuilder {
	b.account.instrumentCode = instrumentCode
	return b
}

// WithDimension sets the dimension.
func (b *InternalBankAccountBuilder) WithDimension(dimension string) *InternalBankAccountBuilder {
	b.account.dimension = dimension
	return b
}

// WithStatus sets the account status.
func (b *InternalBankAccountBuilder) WithStatus(status AccountStatus) *InternalBankAccountBuilder {
	b.account.status = status
	return b
}

// WithCorrespondent sets the correspondent bank details.
func (b *InternalBankAccountBuilder) WithCorrespondent(correspondent *CorrespondentDetails) *InternalBankAccountBuilder {
	b.account.correspondent = correspondent
	return b
}

// WithAttributes sets the metadata attributes.
func (b *InternalBankAccountBuilder) WithAttributes(attributes map[string]string) *InternalBankAccountBuilder {
	if attributes != nil {
		b.account.attributes = make(map[string]string, len(attributes))
		for k, v := range attributes {
			b.account.attributes[k] = v
		}
	}
	return b
}

// WithVersion sets the version number.
func (b *InternalBankAccountBuilder) WithVersion(version int64) *InternalBankAccountBuilder {
	b.account.version = version
	return b
}

// WithCreatedAt sets the creation timestamp.
func (b *InternalBankAccountBuilder) WithCreatedAt(createdAt time.Time) *InternalBankAccountBuilder {
	b.account.createdAt = createdAt
	return b
}

// WithUpdatedAt sets the last update timestamp.
func (b *InternalBankAccountBuilder) WithUpdatedAt(updatedAt time.Time) *InternalBankAccountBuilder {
	b.account.updatedAt = updatedAt
	return b
}

// Build returns the constructed InternalBankAccount.
// This is used for persistence reconstruction and does not validate.
func (b *InternalBankAccountBuilder) Build() InternalBankAccount {
	return b.account
}
