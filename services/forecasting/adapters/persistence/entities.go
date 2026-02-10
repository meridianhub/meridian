// Package persistence provides CockroachDB persistence implementations for the Forecasting service.
package persistence

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// ForecastingStrategyEntity represents a forecasting strategy row in the database.
type ForecastingStrategyEntity struct {
	ID                         uuid.UUID
	TenantID                   string
	Name                       string
	Description                sql.NullString
	StarlarkCode               string
	HorizonHours               int
	GranularityHours           int
	Schedule                   string
	InputDatasetCodes          []string
	OutputDatasetCode          string
	ReferenceDataResolutionKey sql.NullString
	Status                     string
	Version                    int64
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}
