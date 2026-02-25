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

	// Handle nullable counterparty fields
	var counterpartyID, counterpartyName, counterpartyExternalRef *string
	if counterparty := account.Counterparty(); counterparty != nil {
		id := counterparty.CounterpartyID()
		name := counterparty.CounterpartyName()
		externalRef := counterparty.ExternalRef()
		counterpartyID = &id
		counterpartyName = &name
		counterpartyExternalRef = &externalRef
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
		ID:                      account.ID(),
		AccountID:               account.AccountID(),
		AccountCode:             account.AccountCode(),
		Name:                    account.Name(),
		AccountType:             string(account.AccountType()),
		ClearingPurpose:         clearingPurpose,
		OrgPartyID:              account.OrgPartyID(),
		ProductTypeCode:         productTypeCode,
		ProductTypeVersion:      productTypeVersion,
		InstrumentCode:          account.InstrumentCode(),
		Dimension:               account.Dimension(),
		Status:                  string(account.Status()),
		CounterpartyID:          counterpartyID,
		CounterpartyName:        counterpartyName,
		CounterpartyExternalRef: counterpartyExternalRef,
		Attributes:              attributes,
		Version:                 account.Version(),
		CreatedAt:               account.CreatedAt(),
		UpdatedAt:               account.UpdatedAt(),
		CreatedBy:               auditUser,
		UpdatedBy:               auditUser,
	}
}

// toDomain converts a persistence entity to a domain InternalAccount.
func toDomain(entity *InternalAccountEntity) domain.InternalAccount {
	// Handle counterparty details reconstruction
	var counterparty *domain.CounterpartyDetails
	if entity.CounterpartyID != nil && entity.CounterpartyName != nil && entity.CounterpartyExternalRef != nil {
		// Reconstruct counterparty details from persistence
		counterparty = reconstructCounterparty(
			*entity.CounterpartyID,
			*entity.CounterpartyName,
			*entity.CounterpartyExternalRef,
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
		WithCounterparty(counterparty).
		WithAttributes(attributes).
		WithVersion(entity.Version).
		WithCreatedAt(entity.CreatedAt).
		WithUpdatedAt(entity.UpdatedAt).
		Build()
}

// reconstructCounterparty creates a CounterpartyDetails from persisted values.
// This bypasses normal validation since we trust persisted data.
func reconstructCounterparty(counterpartyID, counterpartyName, externalRef string) *domain.CounterpartyDetails {
	// Use the domain constructor - persisted data should always be valid
	details, err := domain.NewCounterpartyDetails(counterpartyID, counterpartyName, externalRef)
	if err != nil {
		// Log at warn level - this indicates data corruption that needs investigation.
		// Return nil to gracefully handle corrupt data without failing the request.
		slog.Warn("corrupt counterparty data in database",
			"counterpartyID", counterpartyID,
			"counterpartyName", counterpartyName,
			"externalRef", externalRef,
			"error", err,
		)
		return nil
	}
	return details
}
