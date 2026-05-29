// Package persistence - entity/domain mapping helpers for the account repository.
package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// toEntity converts domain model to database entity
// Note: The entity schema matches migrations/current_account/*.sql
// OverdraftEnabled is derived from OverdraftLimit > 0
func toEntity(ctx context.Context, account domain.CurrentAccount) (*CurrentAccountEntity, error) {
	// Parse PartyID as UUID - domain model uses string for flexibility
	partyUUID, err := uuid.Parse(account.PartyID())
	if err != nil {
		return nil, fmt.Errorf("invalid party ID %q: %w", account.PartyID(), err)
	}

	// Extract audit user from context (falls back to "system" if not available)
	auditUser := audit.GetUserFromContext(ctx)

	// Convert domain StatusHistory to persistence StatusHistoryJSON
	domainHistory := account.StatusHistory()
	statusHistory := make(StatusHistoryJSON, len(domainHistory))
	for i, change := range domainHistory {
		statusHistory[i] = StatusHistoryEntry{
			FromStatus: string(change.From),
			ToStatus:   string(change.To),
			Reason:     change.Reason,
			Timestamp:  change.Timestamp,
			ChangedBy:  auditUser,
		}
	}

	// Handle freeze reason - nil if empty
	var freezeReason *string
	if account.FreezeReason() != "" {
		reason := account.FreezeReason()
		freezeReason = &reason
	}

	// Map product type fields (nil if empty for backwards compatibility)
	var productTypeCode *string
	var productTypeVersion *int
	if account.ProductTypeCode() != "" {
		code := account.ProductTypeCode()
		productTypeCode = &code
		version := account.ProductTypeVersion()
		productTypeVersion = &version
	}

	// Map behavior class (nil for legacy accounts without product type)
	var behaviorClass *string
	if account.BehaviorClass() != "" {
		bc := account.BehaviorClass()
		behaviorClass = &bc
	}

	// ToMinorUnitsUnchecked is safe here: domain layer validates amounts before persistence,
	// so overflow (>92 quadrillion cents) cannot occur for valid accounts
	// Note: Balance fields are not persisted to DB (gorm:"-") but kept on entity for
	// in-memory round-trip. Position Keeping is now the source of truth for balances.
	return &CurrentAccountEntity{
		ID:                    account.ID(),
		AccountID:             account.AccountID(),          // Business account identifier
		AccountIdentification: account.ExternalIdentifier(), // IBAN stored in account_identification
		AccountType:           "current",                    // Default for current accounts
		InstrumentCode:        account.InstrumentCode(),
		Dimension:             account.Dimension(),
		Precision:             account.Balance().Precision(),
		Status:                string(account.Status()),
		PartyID:               partyUUID,
		OrgPartyID:            account.OrgPartyID(),
		OverdraftLimit:        0, // Overdraft is now product-type behavior, not domain state
		OverdraftRate:         0, // Overdraft is now product-type behavior, not domain state
		ProductTypeCode:       productTypeCode,
		ProductTypeVersion:    productTypeVersion,
		BehaviorClass:         behaviorClass,
		Balance:               account.Balance().ToMinorUnitsUnchecked(),          // gorm:"-" - not persisted
		AvailableBalance:      account.AvailableBalance().ToMinorUnitsUnchecked(), // gorm:"-" - not persisted
		FreezeReason:          freezeReason,
		StatusHistory:         statusHistory,
		Version:               account.Version(),
		CreatedAt:             account.CreatedAt(),
		UpdatedAt:             account.UpdatedAt(),
		CreatedBy:             auditUser,
		UpdatedBy:             auditUser,
	}, nil
}

// toDomain converts database entity to domain model using the builder pattern.
// Note: Balance fields are not persisted - balance computation delegated to Position Keeping service.
// The service layer is responsible for populating balance from Position Keeping after retrieval.
func toDomain(entity *CurrentAccountEntity) (domain.CurrentAccount, error) {
	// Balance fields are no longer persisted to the database.
	// Use entity's in-memory balance fields if populated (e.g., from recent save),
	// otherwise initialize with zero values.
	// The service layer should populate from Position Keeping for authoritative balance.
	balance, err := domain.NewAmountFromInstrument(entity.InstrumentCode, entity.Dimension, entity.Precision, entity.Balance)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create balance: %w", err)
	}
	availableBalance, err := domain.NewAmountFromInstrument(entity.InstrumentCode, entity.Dimension, entity.Precision, entity.AvailableBalance)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create available balance: %w", err)
	}

	// Balance is now computed by Position Keeping service, so use current time as placeholder.
	// The service layer will update this when fetching balance from Position Keeping.
	balanceUpdatedAt := entity.UpdatedAt

	// Convert persistence StatusHistoryJSON to domain StatusHistory
	var statusHistory []domain.StatusChange
	if len(entity.StatusHistory) > 0 {
		statusHistory = make([]domain.StatusChange, len(entity.StatusHistory))
		for i, entry := range entity.StatusHistory {
			statusHistory[i] = domain.StatusChange{
				From:      domain.AccountStatus(entry.FromStatus),
				To:        domain.AccountStatus(entry.ToStatus),
				Reason:    entry.Reason,
				Timestamp: entry.Timestamp,
				ChangedBy: entry.ChangedBy,
			}
		}
	}

	// Handle freeze reason - empty string if nil
	freezeReason := ""
	if entity.FreezeReason != nil {
		freezeReason = *entity.FreezeReason
	}

	// Map product type fields (empty string / 0 if nil for domain model)
	productTypeCode := ""
	productTypeVersion := 0
	if entity.ProductTypeCode != nil {
		productTypeCode = *entity.ProductTypeCode
	}
	if entity.ProductTypeVersion != nil {
		productTypeVersion = *entity.ProductTypeVersion
	}

	// Map behavior class (empty string for legacy accounts without product type)
	behaviorClass := ""
	if entity.BehaviorClass != nil {
		behaviorClass = *entity.BehaviorClass
	}

	// Use builder pattern to construct immutable domain model
	// Note: Balance comes from entity's in-memory fields (gorm:"-") if populated,
	// otherwise zero. Service layer should fetch authoritative balance from Position Keeping.
	return domain.NewCurrentAccountBuilder().
		WithID(entity.ID).
		WithAccountID(entity.AccountID).
		WithExternalIdentifier(entity.AccountIdentification).
		WithInstrumentCode(entity.InstrumentCode).
		WithDimension(entity.Dimension).
		WithPartyID(entity.PartyID.String()).
		WithOrgPartyID(entity.OrgPartyID).
		WithBalance(balance).
		WithAvailableBalance(availableBalance).
		WithStatus(domain.AccountStatus(entity.Status)).
		WithFreezeReason(freezeReason).
		WithStatusHistory(statusHistory).
		WithBalanceUpdatedAt(balanceUpdatedAt).
		WithVersion(entity.Version).
		WithCreatedAt(entity.CreatedAt).
		WithUpdatedAt(entity.UpdatedAt).
		WithProductTypeCode(productTypeCode).
		WithProductTypeVersion(productTypeVersion).
		WithBehaviorClass(behaviorClass).
		Build(), nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
// This handles the race condition where two concurrent creates attempt to insert
// the same account_identification (IBAN).
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// PostgreSQL unique violation error code is 23505
	// GORM wraps this, so we check the error message
	errStr := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
