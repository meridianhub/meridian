// Package persistence - entity/domain conversion helpers for ledger postings and booking logs.
package persistence

import (
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

// decimalFromMinorUnits converts minor units (int64) to decimal amount based on precision.
// For precision 2: 10050 -> 100.50
// For precision 0: 100 -> 100
// For precision 6: 1234567 -> 1.234567
func decimalFromMinorUnits(minorUnits int64, precision int) decimal.Decimal {
	divisor := decimal.New(1, int32(precision))
	return decimal.NewFromInt(minorUnits).Div(divisor)
}

// decimalToMinorUnits converts a decimal amount to minor units based on precision.
// For precision 2: 100.50 -> 10050
// For precision 0: 100 -> 100
// For precision 6: 1.234567 -> 1234567
// Returns error if the result has fractional units that cannot be represented.
func decimalToMinorUnits(amount decimal.Decimal, precision int) (int64, error) {
	multiplier := decimal.New(1, int32(precision))
	scaled := amount.Mul(multiplier)

	// Validate no fractional units
	if !scaled.Equal(scaled.Truncate(0)) {
		return 0, ErrFractionalCents
	}

	return scaled.IntPart(), nil
}

// toPostingEntity converts domain model to database entity.
//
// The conversion extracts instrument metadata from the Money type and stores it
// in the entity for reconstruction during retrieval. This supports multi-asset
// quantities including both monetary (USD, EUR) and commodity (KWH, GPU_HOUR) instruments.
func toPostingEntity(posting *domain.LedgerPosting) (LedgerPostingEntity, error) {
	// Extract instrument from the Money type
	instrument := posting.Amount.Instrument

	// Convert decimal amount to minor units based on instrument precision
	amountMinorUnits, err := decimalToMinorUnits(posting.Amount.Amount, instrument.Precision)
	if err != nil {
		return LedgerPostingEntity{}, err
	}

	// Handle nil attributes - use empty map for JSONB default
	attributes := posting.Attributes
	if attributes == nil {
		attributes = make(map[string]string)
	}

	return LedgerPostingEntity{
		ID:                    posting.ID,
		FinancialBookingLogID: posting.FinancialBookingLogID,
		PostingDirection:      string(posting.Direction),
		AmountMinorUnits:      amountMinorUnits,
		Currency:              instrument.Code,      // Instrument code (e.g., "USD", "KWH")
		DimensionType:         instrument.Dimension, // "CURRENCY", "ENERGY", etc.
		InstrumentVersion:     instrument.Version,   // Schema version
		InstrumentPrecision:   instrument.Precision, // Decimal places
		Attributes:            datatypes.NewJSONType(attributes),
		AccountID:             posting.AccountID,
		AccountServiceDomain:  posting.AccountServiceDomain,
		ValueDate:             posting.ValueDate,
		PostingResult:         posting.PostingResult,
		Status:                string(posting.Status),
		CorrelationID:         posting.CorrelationID,
		CreatedAt:             posting.CreatedAt,
	}, nil
}

// toPostingDomain converts database entity to domain model.
//
// The conversion reconstructs the Instrument from stored fields and creates
// the appropriate Money type. For backward compatibility, missing dimension/version/precision
// fields default to currency values (dimension="CURRENCY", version=1, precision=2).
func toPostingDomain(entity *LedgerPostingEntity) *domain.LedgerPosting {
	instrument, precision := reconstructInstrument(entity)

	// Convert minor units back to decimal based on precision
	amount := decimalFromMinorUnits(entity.AmountMinorUnits, precision)
	money := domain.NewMoney(amount, instrument)

	// Extract attributes from JSONB
	attributes := entity.Attributes.Data()
	if attributes == nil {
		attributes = make(map[string]string)
	}

	return &domain.LedgerPosting{
		ID:                    entity.ID,
		FinancialBookingLogID: entity.FinancialBookingLogID,
		Direction:             domain.PostingDirection(entity.PostingDirection),
		Amount:                money,
		AccountID:             entity.AccountID,
		AccountServiceDomain:  entity.AccountServiceDomain,
		ValueDate:             entity.ValueDate,
		PostingResult:         entity.PostingResult,
		Status:                domain.TransactionStatus(entity.Status),
		CorrelationID:         entity.CorrelationID,
		CreatedAt:             entity.CreatedAt,
		Attributes:            attributes,
	}
}

// reconstructInstrument rebuilds a domain.Instrument from stored entity fields with backward compatibility.
// Returns the instrument and its precision (needed for minor-unit conversion).
func reconstructInstrument(entity *LedgerPostingEntity) (domain.Instrument, int) {
	dimensionType := entity.DimensionType
	if dimensionType == "" {
		dimensionType = domain.DimensionCurrency
	}

	instrumentVersion := entity.InstrumentVersion
	if instrumentVersion == 0 {
		instrumentVersion = 1
	}

	instrumentPrecision := entity.InstrumentPrecision
	if instrumentPrecision == 0 && dimensionType == domain.DimensionCurrency {
		instrumentPrecision = 2
	}

	instrument, err := domain.NewInstrument(
		entity.Currency,
		instrumentVersion,
		dimensionType,
		instrumentPrecision,
	)
	if err != nil {
		// Fallback for backward compatibility
		instrument = domain.Instrument{
			Code:      entity.Currency,
			Version:   instrumentVersion,
			Dimension: dimensionType,
			Precision: instrumentPrecision,
		}
	}

	return instrument, instrumentPrecision
}

// toBookingLogEntity converts domain model to database entity
func toBookingLogEntity(log *domain.FinancialBookingLog, idempotencyKey string) FinancialBookingLogEntity {
	return FinancialBookingLogEntity{
		ID:                      log.ID,
		FinancialAccountType:    log.FinancialAccountType,
		ProductServiceReference: log.ProductServiceReference,
		BusinessUnitReference:   log.BusinessUnitReference,
		ChartOfAccountsRules:    log.ChartOfAccountsRules,
		BaseCurrency:            string(log.BaseCurrency),
		Status:                  string(log.Status),
		IdempotencyKey:          idempotencyKey,
		CreatedAt:               log.CreatedAt,
		UpdatedAt:               log.UpdatedAt,
		Version:                 1,
	}
}

// toBookingLogDomain converts database entity to domain model.
// Note: postings field is unexported and initialized empty.
// Postings are loaded separately to avoid N+1 queries.
func toBookingLogDomain(entity *FinancialBookingLogEntity) *domain.FinancialBookingLog {
	// We need to use NewFinancialBookingLog and then update fields since postings is unexported
	// However, NewFinancialBookingLog creates a new ID, so we reconstruct manually
	log := domain.FinancialBookingLog{
		ID:                      entity.ID,
		FinancialAccountType:    entity.FinancialAccountType,
		ProductServiceReference: entity.ProductServiceReference,
		BusinessUnitReference:   entity.BusinessUnitReference,
		ChartOfAccountsRules:    entity.ChartOfAccountsRules,
		BaseCurrency:            domain.Currency(entity.BaseCurrency),
		Status:                  domain.TransactionStatus(entity.Status),
		CreatedAt:               entity.CreatedAt,
		UpdatedAt:               entity.UpdatedAt,
		// postings initialized as empty slice (loaded separately)
	}
	return &log
}
