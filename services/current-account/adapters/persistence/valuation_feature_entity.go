package persistence

import (
	"time"

	"github.com/google/uuid"
)

// ValuationFeatureEntity represents the database persistence model for valuation features.
// Optimized for database concerns: audit fields, indexes, constraints, bi-temporal validity.
type ValuationFeatureEntity struct {
	// Primary key
	ID uuid.UUID `gorm:"primaryKey"`

	// Foreign key to account table
	AccountID uuid.UUID `gorm:"not null;index:idx_valuation_feature_account"`

	// Input instrument code that will be valued
	InstrumentCode string `gorm:"column:instrument_code;size:32;not null"`

	// Reference to valuation method in Valuation Engine Service
	ValuationMethodID      uuid.UUID `gorm:"column:valuation_method_id;not null;index:idx_valuation_feature_method"`
	ValuationMethodVersion int       `gorm:"column:valuation_method_version;not null"`

	// Method-specific parameters (JSON blob)
	Parameters []byte `gorm:"type:jsonb"`

	// Lifecycle status
	LifecycleStatus string `gorm:"column:lifecycle_status;size:16;not null;check:lifecycle_status IN ('INITIATED','ACTIVE','TERMINATED')"`

	// Bi-temporal validity
	ValidFrom time.Time `gorm:"column:valid_from;not null;default:NOW();index:idx_valuation_feature_temporal"`
	ValidTo   time.Time `gorm:"column:valid_to;not null;default:'9999-12-31 23:59:59+00';index:idx_valuation_feature_temporal"`

	// Audit fields
	CreatedAt time.Time `gorm:"not null"`
	CreatedBy string    `gorm:"size:100;not null"`
	UpdatedAt time.Time `gorm:"not null"`
	UpdatedBy string    `gorm:"size:100;not null"`

	// Optimistic locking
	Version int `gorm:"not null;default:1"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture.
func (ValuationFeatureEntity) TableName() string {
	return "valuation_features"
}
