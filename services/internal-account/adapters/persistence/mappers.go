package persistence

import (
	"context"
	"log/slog"

	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
)

// toEntity converts a domain InternalAccount to a persistence entity.
func toEntity(ctx context.Context, account domain.InternalAccount) *InternalAccountEntity {
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

	// Handle nullable clearing_purpose field
	var clearingPurpose *string
	if cp := account.ClearingPurpose(); cp != "" && cp != domain.ClearingPurposeUnspecified {
		cpStr := string(cp)
		clearingPurpose = &cpStr
	}

	// Handle nullable product type fields
	var productTypeCode *string
	var productTypeVersion *int
	if ptc := account.ProductTypeCode(); ptc != "" {
		productTypeCode = &ptc
		ptv := account.ProductTypeVersion()
		productTypeVersion = &ptv
	}

	return &InternalAccountEntity{
		ID:                       account.ID(),
		AccountID:                account.AccountID(),
		AccountCode:              account.AccountCode(),
		Name:                     account.Name(),
		AccountType:              string(account.AccountType()),
		ClearingPurpose:          clearingPurpose,
		OrgPartyID:               account.OrgPartyID(),
		ProductTypeCode:          productTypeCode,
		ProductTypeVersion:       productTypeVersion,
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

// toDomain converts a persistence entity to a domain InternalAccount.
func toDomain(entity *InternalAccountEntity) domain.InternalAccount {
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

	// Handle nullable clearing_purpose - default to UNSPECIFIED if nil
	clearingPurpose := domain.ClearingPurposeUnspecified
	if entity.ClearingPurpose != nil {
		clearingPurpose = domain.ClearingPurpose(*entity.ClearingPurpose)
	}

	// Handle nullable product type fields
	var productTypeCode string
	var productTypeVersion int
	if entity.ProductTypeCode != nil {
		productTypeCode = *entity.ProductTypeCode
	}
	if entity.ProductTypeVersion != nil {
		productTypeVersion = *entity.ProductTypeVersion
	}

	// Use builder pattern to reconstruct domain model
	return domain.NewInternalAccountBuilder().
		WithID(entity.ID).
		WithAccountID(entity.AccountID).
		WithAccountCode(entity.AccountCode).
		WithName(entity.Name).
		WithAccountType(domain.AccountType(entity.AccountType)).
		WithClearingPurpose(clearingPurpose).
		WithOrgPartyID(entity.OrgPartyID).
		WithProductTypeCode(productTypeCode).
		WithProductTypeVersion(productTypeVersion).
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
