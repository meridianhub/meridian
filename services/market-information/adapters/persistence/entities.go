// Package persistence provides PostgreSQL persistence implementations for the Market Information service.
package persistence

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DataSetDefinitionEntity represents a dataset definition row in the database.
type DataSetDefinitionEntity struct {
	ID                      uuid.UUID
	Code                    string
	Version                 int
	Name                    string
	Description             sql.NullString
	DataCategory            sql.NullString
	ValidationExpression    sql.NullString
	ResolutionKeyExpression string
	ErrorMessageExpression  sql.NullString
	Status                  string
	IsShared                bool
	AccessLevel             string
	CreatedAt               time.Time
	UpdatedAt               time.Time
	ActivatedAt             sql.NullTime
	DeprecatedAt            sql.NullTime
}

// MarketPriceObservationEntity represents a market price observation row in the database.
type MarketPriceObservationEntity struct {
	ID                  uuid.UUID
	DataSetDefinitionID uuid.UUID
	DataSourceID        uuid.UUID
	ResolutionKey       string
	ObservedAt          time.Time
	ValidFrom           sql.NullTime
	ValidTo             sql.NullTime
	CreatedAt           time.Time
	Quality             int
	ObservationContext  []byte // JSONB stored as bytes
	NumericValue        decimal.NullDecimal
	TextValue           sql.NullString
	SupersededBy        uuid.NullUUID
	CausationID         uuid.NullUUID
}

// DataSourceEntity represents a data source row in the database.
type DataSourceEntity struct {
	ID           uuid.UUID
	Code         string
	Name         string
	Description  sql.NullString
	TrustLevel   int
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeprecatedAt sql.NullTime
	Version      int64
}
