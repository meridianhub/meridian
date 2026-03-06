// Package domain contains the domain models for the Market Information service.
package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MarketPriceObservation represents a single market data observation with bi-temporal fields.
// This is an immutable value type - the only mutating operation is Supersede which returns
// a new instance.
//
// Bi-temporal modeling:
//   - ValidFrom/ValidTo: The effective time range when this observation applies (valid time)
//   - CreatedAt: When we learned about this observation (knowledge/transaction time)
//   - SupersededAt: When this observation was replaced by a newer one
//
// The quality ladder enables "Estimates vs. Actuals" reconciliation:
//   - Higher quality observations automatically supersede lower quality ones
//   - ESTIMATE < ACTUAL < VERIFIED
//
// Lineage tracking:
//   - SupersededBy forms a chain linking to the observation that replaced this one
//   - CausationID enables event sourcing correlation
type MarketPriceObservation struct {
	id                 uuid.UUID
	dataSetCode        string             // References DataSetDefinition.Code
	sourceID           uuid.UUID          // References DataSource.ID
	resolutionKey      string             // Unique key from CEL expression evaluation
	value              decimal.Decimal    // High-precision market value
	unit               string             // Unit of measurement (e.g., "USD", "kWh")
	observedAt         time.Time          // When the measurement was taken
	validFrom          time.Time          // Start of effective time range
	validTo            time.Time          // End of effective time range
	createdAt          time.Time          // Knowledge time: when we learned about this
	supersededAt       *time.Time         // When this observation was replaced
	supersededBy       *uuid.UUID         // ID of the observation that replaced this one
	causationID        uuid.UUID          // For event sourcing correlation
	qualityLevel       QualityLevel       // ESTIMATE, ACTUAL, or VERIFIED
	trustLevel         int                // 0-100, derived from DataSource
	observationContext ObservationContext // Typed metadata about collection and processing
}

// NewMarketPriceObservation creates a new MarketPriceObservation with validated fields.
// Returns a value type (not pointer) following the immutability pattern.
//
// Initial state:
//   - SupersededAt: nil
//   - SupersededBy: nil
//
// Validation:
//   - dataSetCode cannot be empty
//   - sourceID cannot be nil UUID
//   - resolutionKey cannot be empty
//   - unit cannot be empty
//   - validFrom must be before validTo
//   - qualityLevel must be valid
//   - trustLevel must be between 0 and 100
//   - causationID cannot be nil UUID
func NewMarketPriceObservation(
	dataSetCode string,
	sourceID uuid.UUID,
	resolutionKey string,
	value decimal.Decimal,
	unit string,
	observedAt time.Time,
	validFrom time.Time,
	validTo time.Time,
	causationID uuid.UUID,
	qualityLevel QualityLevel,
	trustLevel int,
	observationContext ObservationContext,
) (MarketPriceObservation, error) {
	if dataSetCode == "" {
		return MarketPriceObservation{}, ErrDataSetCodeRequired
	}
	if sourceID == uuid.Nil {
		return MarketPriceObservation{}, ErrSourceIDRequired
	}
	if resolutionKey == "" {
		return MarketPriceObservation{}, ErrResolutionKeyRequired
	}
	if unit == "" {
		return MarketPriceObservation{}, ErrUnitRequired
	}
	if !validFrom.Before(validTo) {
		return MarketPriceObservation{}, ErrInvalidTemporalBounds
	}
	if !qualityLevel.IsValid() {
		return MarketPriceObservation{}, ErrInvalidQualityLevel
	}
	if trustLevel < 0 || trustLevel > 100 {
		return MarketPriceObservation{}, ErrInvalidTrustLevel
	}
	if causationID == uuid.Nil {
		return MarketPriceObservation{}, ErrCausationIDRequired
	}

	return MarketPriceObservation{
		id:                 uuid.New(),
		dataSetCode:        dataSetCode,
		sourceID:           sourceID,
		resolutionKey:      resolutionKey,
		value:              value,
		unit:               unit,
		observedAt:         observedAt,
		validFrom:          validFrom,
		validTo:            validTo,
		createdAt:          time.Now(),
		supersededAt:       nil,
		supersededBy:       nil,
		causationID:        causationID,
		qualityLevel:       qualityLevel,
		trustLevel:         trustLevel,
		observationContext: observationContext,
	}, nil
}

// Supersede marks this observation as superseded by another observation.
// Returns a new instance with SupersededAt and SupersededBy set.
// Returns error if the observation is already superseded, the target is nil UUID,
// or the target is the same as this observation's ID.
//
// This method is used when a higher quality observation replaces this one,
// or when the same observation is updated with corrected data.
func (o MarketPriceObservation) Supersede(newObservationID uuid.UUID) (MarketPriceObservation, error) {
	if newObservationID == uuid.Nil || newObservationID == o.id {
		return o, ErrInvalidSupersedeTarget
	}
	if o.IsSuperseded() {
		return o, ErrObservationAlreadySuperseded
	}

	newObs := o
	now := time.Now()
	newObs.supersededAt = &now
	newObs.supersededBy = &newObservationID
	return newObs, nil
}

// IsSuperseded returns true if this observation has been superseded by another.
// Checks both supersededAt and supersededBy for consistency, as the builder
// pattern allows setting either field independently during persistence reconstruction.
func (o MarketPriceObservation) IsSuperseded() bool {
	return o.supersededAt != nil || o.supersededBy != nil
}

// Getters for all unexported fields.

// ID returns the unique identifier.
func (o MarketPriceObservation) ID() uuid.UUID {
	return o.id
}

// DataSetCode returns the dataset code reference.
func (o MarketPriceObservation) DataSetCode() string {
	return o.dataSetCode
}

// SourceID returns the data source identifier.
func (o MarketPriceObservation) SourceID() uuid.UUID {
	return o.sourceID
}

// ResolutionKey returns the unique key from CEL evaluation.
func (o MarketPriceObservation) ResolutionKey() string {
	return o.resolutionKey
}

// Value returns the market observation value as a decimal.
func (o MarketPriceObservation) Value() decimal.Decimal {
	return o.value
}

// Unit returns the unit of measurement.
func (o MarketPriceObservation) Unit() string {
	return o.unit
}

// ObservedAt returns when the measurement was taken.
func (o MarketPriceObservation) ObservedAt() time.Time {
	return o.observedAt
}

// ValidFrom returns the start of the effective time range.
func (o MarketPriceObservation) ValidFrom() time.Time {
	return o.validFrom
}

// ValidTo returns the end of the effective time range.
func (o MarketPriceObservation) ValidTo() time.Time {
	return o.validTo
}

// CreatedAt returns when we learned about this observation.
func (o MarketPriceObservation) CreatedAt() time.Time {
	return o.createdAt
}

// SupersededAt returns when this observation was replaced.
// Returns nil if the observation has not been superseded.
func (o MarketPriceObservation) SupersededAt() *time.Time {
	return o.supersededAt
}

// SupersededBy returns the ID of the observation that replaced this one.
// Returns nil if the observation has not been superseded.
func (o MarketPriceObservation) SupersededBy() *uuid.UUID {
	return o.supersededBy
}

// CausationID returns the event sourcing correlation ID.
func (o MarketPriceObservation) CausationID() uuid.UUID {
	return o.causationID
}

// QualityLevel returns the quality tier of this observation.
func (o MarketPriceObservation) QualityLevel() QualityLevel {
	return o.qualityLevel
}

// TrustLevel returns the trust score (0-100) from the data source.
func (o MarketPriceObservation) TrustLevel() int {
	return o.trustLevel
}

// ObservationContext returns the typed metadata about collection and processing.
func (o MarketPriceObservation) ObservationContext() ObservationContext {
	return o.observationContext
}

// MarketPriceObservationBuilder provides a builder pattern for reconstructing
// MarketPriceObservation from the persistence layer. This bypasses normal validation
// since we assume persisted data was already validated.
type MarketPriceObservationBuilder struct {
	observation MarketPriceObservation
}

// NewMarketPriceObservationBuilder creates a new builder for MarketPriceObservation reconstruction.
func NewMarketPriceObservationBuilder() *MarketPriceObservationBuilder {
	return &MarketPriceObservationBuilder{
		observation: MarketPriceObservation{},
	}
}

// WithID sets the unique identifier.
func (b *MarketPriceObservationBuilder) WithID(id uuid.UUID) *MarketPriceObservationBuilder {
	b.observation.id = id
	return b
}

// WithDataSetCode sets the dataset code reference.
func (b *MarketPriceObservationBuilder) WithDataSetCode(code string) *MarketPriceObservationBuilder {
	b.observation.dataSetCode = code
	return b
}

// WithSourceID sets the data source identifier.
func (b *MarketPriceObservationBuilder) WithSourceID(id uuid.UUID) *MarketPriceObservationBuilder {
	b.observation.sourceID = id
	return b
}

// WithResolutionKey sets the resolution key.
func (b *MarketPriceObservationBuilder) WithResolutionKey(key string) *MarketPriceObservationBuilder {
	b.observation.resolutionKey = key
	return b
}

// WithValue sets the observation value.
func (b *MarketPriceObservationBuilder) WithValue(value decimal.Decimal) *MarketPriceObservationBuilder {
	b.observation.value = value
	return b
}

// WithUnit sets the unit of measurement.
func (b *MarketPriceObservationBuilder) WithUnit(unit string) *MarketPriceObservationBuilder {
	b.observation.unit = unit
	return b
}

// WithObservedAt sets when the measurement was taken.
func (b *MarketPriceObservationBuilder) WithObservedAt(t time.Time) *MarketPriceObservationBuilder {
	b.observation.observedAt = t
	return b
}

// WithValidFrom sets the start of the effective time range.
func (b *MarketPriceObservationBuilder) WithValidFrom(t time.Time) *MarketPriceObservationBuilder {
	b.observation.validFrom = t
	return b
}

// WithValidTo sets the end of the effective time range.
func (b *MarketPriceObservationBuilder) WithValidTo(t time.Time) *MarketPriceObservationBuilder {
	b.observation.validTo = t
	return b
}

// WithCreatedAt sets when we learned about this observation.
func (b *MarketPriceObservationBuilder) WithCreatedAt(t time.Time) *MarketPriceObservationBuilder {
	b.observation.createdAt = t
	return b
}

// WithSupersededAt sets when this observation was replaced.
func (b *MarketPriceObservationBuilder) WithSupersededAt(t *time.Time) *MarketPriceObservationBuilder {
	b.observation.supersededAt = t
	return b
}

// WithSupersededBy sets the ID of the observation that replaced this one.
func (b *MarketPriceObservationBuilder) WithSupersededBy(id *uuid.UUID) *MarketPriceObservationBuilder {
	b.observation.supersededBy = id
	return b
}

// WithCausationID sets the event sourcing correlation ID.
func (b *MarketPriceObservationBuilder) WithCausationID(id uuid.UUID) *MarketPriceObservationBuilder {
	b.observation.causationID = id
	return b
}

// WithQualityLevel sets the quality tier.
func (b *MarketPriceObservationBuilder) WithQualityLevel(level QualityLevel) *MarketPriceObservationBuilder {
	b.observation.qualityLevel = level
	return b
}

// WithTrustLevel sets the trust score.
func (b *MarketPriceObservationBuilder) WithTrustLevel(level int) *MarketPriceObservationBuilder {
	b.observation.trustLevel = level
	return b
}

// WithObservationContext sets the observation context metadata.
func (b *MarketPriceObservationBuilder) WithObservationContext(ctx ObservationContext) *MarketPriceObservationBuilder {
	b.observation.observationContext = ctx
	return b
}

// Build returns the constructed MarketPriceObservation.
// This is used for persistence reconstruction and does not validate.
func (b *MarketPriceObservationBuilder) Build() MarketPriceObservation {
	return b.observation
}
