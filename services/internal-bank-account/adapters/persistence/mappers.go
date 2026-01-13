package persistence

import (
	"context"
	"log/slog"

	"github.com/meridianhub/meridian/services/internal-bank-account/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
)

// toEntity converts a domain InternalBankAccount to a persistence entity.
func toEntity(ctx context.Context, account domain.InternalBankAccount) *InternalBankAccountEntity {
	auditUser := audit.GetUserFromContext(ctx)

	// Handle nullable correspondent fields
	var correspondentBankID, correspondentBankName, correspondentExternalRef *string
	if correspondent := account.Correspondent(); correspondent != nil {
		bankID := correspondent.BankID()
		bankName := correspondent.BankName()
		externalRef := correspondent.ExternalAccountRef()
		correspondentBankID = &bankID
		correspondentBankName = &bankName
		correspondentExternalRef = &externalRef
	}

	// Convert attributes map
	var attributes AttributesJSON
	if attrs := account.Attributes(); attrs != nil {
		attributes = make(AttributesJSON, len(attrs))
		for k, v := range attrs {
			attributes[k] = v
		}
	} else {
		attributes = make(AttributesJSON)
	}

	return &InternalBankAccountEntity{
		ID:                       account.ID(),
		AccountID:                account.AccountID(),
		AccountCode:              account.AccountCode(),
		Name:                     account.Name(),
		AccountType:              string(account.AccountType()),
		InstrumentCode:           account.InstrumentCode(),
		Dimension:                account.Dimension(),
		Status:                   string(account.Status()),
		CorrespondentBankID:      correspondentBankID,
		CorrespondentBankName:    correspondentBankName,
		CorrespondentExternalRef: correspondentExternalRef,
		Attributes:               attributes,
		Version:                  account.Version(),
		CreatedAt:                account.CreatedAt(),
		UpdatedAt:                account.UpdatedAt(),
		CreatedBy:                auditUser,
		UpdatedBy:                auditUser,
	}
}

// toDomain converts a persistence entity to a domain InternalBankAccount.
func toDomain(entity *InternalBankAccountEntity) domain.InternalBankAccount {
	// Handle correspondent details reconstruction
	var correspondent *domain.CorrespondentDetails
	if entity.CorrespondentBankID != nil && entity.CorrespondentBankName != nil && entity.CorrespondentExternalRef != nil {
		// Reconstruct correspondent details from persistence
		// Use empty swift code and nil attributes since we don't persist those in the main table
		correspondent = reconstructCorrespondent(
			*entity.CorrespondentBankID,
			*entity.CorrespondentBankName,
			*entity.CorrespondentExternalRef,
		)
	}

	// Convert JSONB attributes to map
	var attributes map[string]string
	if len(entity.Attributes) > 0 {
		attributes = make(map[string]string, len(entity.Attributes))
		for k, v := range entity.Attributes {
			attributes[k] = v
		}
	}

	// Use builder pattern to reconstruct domain model
	return domain.NewInternalBankAccountBuilder().
		WithID(entity.ID).
		WithAccountID(entity.AccountID).
		WithAccountCode(entity.AccountCode).
		WithName(entity.Name).
		WithAccountType(domain.AccountType(entity.AccountType)).
		WithInstrumentCode(entity.InstrumentCode).
		WithDimension(entity.Dimension).
		WithStatus(domain.AccountStatus(entity.Status)).
		WithCorrespondent(correspondent).
		WithAttributes(attributes).
		WithVersion(entity.Version).
		WithCreatedAt(entity.CreatedAt).
		WithUpdatedAt(entity.UpdatedAt).
		Build()
}

// reconstructCorrespondent creates a CorrespondentDetails from persisted values.
// This bypasses normal validation since we trust persisted data.
func reconstructCorrespondent(bankID, bankName, externalRef string) *domain.CorrespondentDetails {
	// Use the domain constructor - persisted data should always be valid
	details, err := domain.NewCorrespondentDetails(bankID, bankName, externalRef)
	if err != nil {
		// Log at warn level - this indicates data corruption that needs investigation.
		// Return nil to gracefully handle corrupt data without failing the request.
		slog.Warn("corrupt correspondent data in database",
			"bankID", bankID,
			"bankName", bankName,
			"externalRef", externalRef,
			"error", err,
		)
		return nil
	}
	return details
}
