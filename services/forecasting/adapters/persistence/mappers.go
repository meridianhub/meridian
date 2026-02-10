// Package persistence provides CockroachDB persistence implementations for the Forecasting service.
package persistence

import (
	"database/sql"

	"github.com/meridianhub/meridian/services/forecasting/domain"
)

// StrategyToEntity converts a domain ForecastingStrategy to a database entity.
func StrategyToEntity(s domain.ForecastingStrategy) ForecastingStrategyEntity {
	entity := ForecastingStrategyEntity{
		ID:                s.ID(),
		TenantID:          s.TenantID(),
		Name:              s.Name(),
		StarlarkCode:      s.StarlarkCode(),
		HorizonHours:      s.HorizonHours(),
		GranularityHours:  s.GranularityHours(),
		Schedule:          s.Schedule(),
		InputDatasetCodes: s.InputDatasetCodes(),
		OutputDatasetCode: s.OutputDatasetCode(),
		Status:            s.Status().String(),
		Version:           s.Version(),
		CreatedAt:         s.CreatedAt(),
		UpdatedAt:         s.UpdatedAt(),
	}

	if s.Description() != "" {
		entity.Description = sql.NullString{String: s.Description(), Valid: true}
	}
	if s.ReferenceDataResolutionKey() != "" {
		entity.ReferenceDataResolutionKey = sql.NullString{String: s.ReferenceDataResolutionKey(), Valid: true}
	}

	return entity
}

// EntityToStrategy converts a database entity to a domain ForecastingStrategy.
func EntityToStrategy(e ForecastingStrategyEntity) domain.ForecastingStrategy {
	builder := domain.NewForecastingStrategyBuilder().
		WithID(e.ID).
		WithTenantID(e.TenantID).
		WithName(e.Name).
		WithStarlarkCode(e.StarlarkCode).
		WithHorizonHours(e.HorizonHours).
		WithGranularityHours(e.GranularityHours).
		WithSchedule(e.Schedule).
		WithInputDatasetCodes(e.InputDatasetCodes).
		WithOutputDatasetCode(e.OutputDatasetCode).
		WithStatus(parseStrategyStatus(e.Status)).
		WithVersion(e.Version).
		WithCreatedAt(e.CreatedAt).
		WithUpdatedAt(e.UpdatedAt)

	if e.Description.Valid {
		builder.WithDescription(e.Description.String)
	}
	if e.ReferenceDataResolutionKey.Valid {
		builder.WithReferenceDataResolutionKey(e.ReferenceDataResolutionKey.String)
	}

	return builder.Build()
}

// parseStrategyStatus converts a string status to domain.StrategyStatus.
func parseStrategyStatus(s string) domain.StrategyStatus {
	switch s {
	case "DRAFT":
		return domain.StrategyStatusDraft
	case "ACTIVE":
		return domain.StrategyStatusActive
	case "DEPRECATED":
		return domain.StrategyStatusDeprecated
	default:
		return domain.StrategyStatusDraft
	}
}
