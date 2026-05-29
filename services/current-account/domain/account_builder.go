// Package domain - builder for reconstructing CurrentAccount instances from persistence.
package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

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
