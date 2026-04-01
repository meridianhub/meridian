// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/shopspring/decimal"
)

// DataSetDefinitionToEntity converts a domain DataSetDefinition to a database entity.
func DataSetDefinitionToEntity(d domain.DataSetDefinition) DataSetDefinitionEntity {
	entity := DataSetDefinitionEntity{
		ID:                      d.ID(),
		Code:                    d.Code(),
		Version:                 d.Version(),
		Name:                    d.Name(),
		ResolutionKeyExpression: d.ResolutionKeyExpression(),
		Status:                  d.Status().String(),
		IsShared:                d.IsShared(),
		AccessLevel:             d.AccessLevel().String(),
		CreatedAt:               d.CreatedAt(),
		UpdatedAt:               d.UpdatedAt(),
	}

	if d.Description() != "" {
		entity.Description = sql.NullString{String: d.Description(), Valid: true}
	}
	if d.DataCategory().String() != "" {
		entity.DataCategory = sql.NullString{String: d.DataCategory().String(), Valid: true}
	}
	if d.ValidationExpression() != "" {
		entity.ValidationExpression = sql.NullString{String: d.ValidationExpression(), Valid: true}
	}
	if d.ErrorMessageExpression() != "" {
		entity.ErrorMessageExpression = sql.NullString{String: d.ErrorMessageExpression(), Valid: true}
	}
	if d.ActivatedAt() != nil {
		entity.ActivatedAt = sql.NullTime{Time: *d.ActivatedAt(), Valid: true}
	}
	if d.DeprecatedAt() != nil {
		entity.DeprecatedAt = sql.NullTime{Time: *d.DeprecatedAt(), Valid: true}
	}

	return entity
}

// EntityToDataSetDefinition converts a database entity to a domain DataSetDefinition.
func EntityToDataSetDefinition(e DataSetDefinitionEntity) domain.DataSetDefinition {
	builder := domain.NewDataSetDefinitionBuilder().
		WithID(e.ID).
		WithCode(e.Code).
		WithVersion(e.Version).
		WithName(e.Name).
		WithResolutionKeyExpression(e.ResolutionKeyExpression).
		WithStatus(parseDataSetStatus(e.Status)).
		WithIsShared(e.IsShared).
		WithAccessLevel(parseAccessLevel(e.AccessLevel)).
		WithCreatedAt(e.CreatedAt).
		WithUpdatedAt(e.UpdatedAt)

	if e.Description.Valid {
		builder.WithDescription(e.Description.String)
	}
	if e.DataCategory.Valid {
		builder.WithDataCategory(domain.DataCategory(e.DataCategory.String))
	}
	if e.ValidationExpression.Valid {
		builder.WithValidationExpression(e.ValidationExpression.String)
	}
	if e.ErrorMessageExpression.Valid {
		builder.WithErrorMessageExpression(e.ErrorMessageExpression.String)
	}
	if e.ActivatedAt.Valid {
		activatedAt := e.ActivatedAt.Time
		builder.WithActivatedAt(&activatedAt)
	}
	if e.DeprecatedAt.Valid {
		deprecatedAt := e.DeprecatedAt.Time
		builder.WithDeprecatedAt(&deprecatedAt)
	}

	return builder.Build()
}

// parseDataSetStatus converts a string status to domain.DataSetStatus.
func parseDataSetStatus(s string) domain.DataSetStatus {
	switch s {
	case "DRAFT":
		return domain.DataSetStatusDraft
	case "ACTIVE":
		return domain.DataSetStatusActive
	case "DEPRECATED":
		return domain.DataSetStatusDeprecated
	default:
		return domain.DataSetStatusDraft
	}
}

// parseAccessLevel converts a string to domain.DataAccessLevel with validation.
// Returns AccessLevelPrivate (the safest default) if the value is invalid.
func parseAccessLevel(s string) domain.DataAccessLevel {
	level := domain.DataAccessLevel(s)
	if !level.IsValid() {
		return domain.AccessLevelPrivate
	}
	return level
}

// DataSourceToEntity converts a domain DataSource to a database entity.
func DataSourceToEntity(s domain.DataSource) DataSourceEntity {
	entity := DataSourceEntity{
		ID:         s.ID(),
		Code:       s.Code(),
		Name:       s.Name(),
		TrustLevel: s.TrustLevel(),
		Status:     s.Status().String(),
		CreatedAt:  s.CreatedAt(),
		UpdatedAt:  s.UpdatedAt(),
		Version:    1, // Default version for new entities
	}

	if s.Description() != "" {
		entity.Description = sql.NullString{String: s.Description(), Valid: true}
	}
	if s.DeprecatedAt() != nil {
		entity.DeprecatedAt = sql.NullTime{Time: *s.DeprecatedAt(), Valid: true}
	}

	return entity
}

// EntityToDataSource converts a database entity to a domain DataSource.
// Sources loaded from DB are always active (soft-deleted sources are excluded by WHERE deleted_at IS NULL).
func EntityToDataSource(e DataSourceEntity) domain.DataSource {
	status := parseDataSourceStatus(e.Status)
	isActive := status == domain.DataSourceStatusActive

	builder := domain.NewDataSourceBuilder().
		WithID(e.ID).
		WithCode(e.Code).
		WithName(e.Name).
		WithTrustLevel(e.TrustLevel).
		WithStatus(status).
		WithIsActive(isActive).
		WithCreatedAt(e.CreatedAt).
		WithUpdatedAt(e.UpdatedAt)

	if e.Description.Valid {
		builder.WithDescription(e.Description.String)
	}
	if e.DeprecatedAt.Valid {
		deprecatedAt := e.DeprecatedAt.Time
		builder.WithDeprecatedAt(&deprecatedAt)
	}

	return builder.Build()
}

// parseDataSourceStatus converts a string status to domain.DataSourceStatus.
func parseDataSourceStatus(s string) domain.DataSourceStatus {
	switch s {
	case "ACTIVE":
		return domain.DataSourceStatusActive
	case "DEPRECATED":
		return domain.DataSourceStatusDeprecated
	default:
		return domain.DataSourceStatusActive
	}
}

// ObservationToEntity converts a domain MarketPriceObservation to a database entity.
// The dataSetDefinitionID parameter is required because the domain model uses code, not ID.
func ObservationToEntity(o domain.MarketPriceObservation, dataSetDefinitionID uuid.UUID) MarketPriceObservationEntity {
	entity := MarketPriceObservationEntity{
		ID:                  o.ID(),
		DataSetDefinitionID: dataSetDefinitionID,
		DataSourceID:        o.SourceID(),
		ResolutionKey:       o.ResolutionKey(),
		ObservedAt:          o.ObservedAt(),
		CreatedAt:           o.CreatedAt(),
		Quality:             o.QualityLevel().Int(),
		ObservationContext:  marshalObservationContext(o.ObservationContext()),
	}

	// Set numeric value from decimal
	entity.NumericValue = decimal.NullDecimal{
		Decimal: o.Value(),
		Valid:   true,
	}

	// Set valid time bounds
	if !o.ValidFrom().IsZero() {
		entity.ValidFrom = sql.NullTime{Time: o.ValidFrom(), Valid: true}
	}
	if !o.ValidTo().IsZero() {
		entity.ValidTo = sql.NullTime{Time: o.ValidTo(), Valid: true}
	}

	// Set supersession info
	if o.SupersededBy() != nil {
		entity.SupersededBy = uuid.NullUUID{UUID: *o.SupersededBy(), Valid: true}
	}

	// Set causation ID
	if o.CausationID() != uuid.Nil {
		entity.CausationID = uuid.NullUUID{UUID: o.CausationID(), Valid: true}
	}

	return entity
}

// EntityToObservation converts a database entity to a domain MarketPriceObservation.
// The dataSetCode and trustLevel parameters are required because they're not stored in the observation table.
func EntityToObservation(e MarketPriceObservationEntity, dataSetCode string, trustLevel int) domain.MarketPriceObservation {
	builder := domain.NewMarketPriceObservationBuilder().
		WithID(e.ID).
		WithDataSetCode(dataSetCode).
		WithSourceID(e.DataSourceID).
		WithResolutionKey(e.ResolutionKey).
		WithObservedAt(e.ObservedAt).
		WithCreatedAt(e.CreatedAt).
		WithQualityLevel(domain.QualityLevel(e.Quality)).
		WithTrustLevel(trustLevel)

	// Set value
	if e.NumericValue.Valid {
		builder.WithValue(e.NumericValue.Decimal)
	}

	// Set valid time bounds
	if e.ValidFrom.Valid {
		builder.WithValidFrom(e.ValidFrom.Time)
	}
	if e.ValidTo.Valid {
		builder.WithValidTo(e.ValidTo.Time)
	}

	// Set supersession info
	if e.SupersededBy.Valid {
		supersededBy := e.SupersededBy.UUID
		builder.WithSupersededBy(&supersededBy)
	}

	// Set causation ID
	if e.CausationID.Valid {
		builder.WithCausationID(e.CausationID.UUID)
	}

	// Set observation context (backward-compatible with empty/null JSONB)
	builder.WithObservationContext(unmarshalObservationContext(e.ObservationContext))

	return builder.Build()
}

// marshalObservationContext serializes an ObservationContext to JSON bytes for JSONB storage.
// Returns "{}" for empty contexts to maintain a valid JSONB value.
func marshalObservationContext(ctx domain.ObservationContext) []byte {
	if ctx.IsEmpty() {
		return []byte("{}")
	}
	data, err := json.Marshal(ctx)
	if err != nil {
		return []byte("{}")
	}
	return data
}

// unmarshalObservationContext deserializes JSON bytes from JSONB into an ObservationContext.
// Returns an empty ObservationContext for nil, empty, or invalid JSON (backward compatibility
// with existing records that store "{}").
func unmarshalObservationContext(data []byte) domain.ObservationContext {
	if len(data) == 0 {
		return domain.ObservationContext{}
	}
	var ctx domain.ObservationContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return domain.ObservationContext{}
	}
	return ctx
}
