// Package manifest provides manifest version history storage and retrieval.
package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

var (
	// ErrNilDatabase is returned when database connection is nil.
	ErrNilDatabase = errors.New("database connection cannot be nil")
	// ErrVersionNotFound is returned when a manifest version is not found.
	ErrVersionNotFound = errors.New("manifest version not found")
	// ErrSequenceConflict is returned when optimistic locking detects a concurrent modification.
	ErrSequenceConflict = errors.New("sequence number conflict: manifest was modified concurrently")
)

// ApplyStatus represents the outcome of applying a manifest.
type ApplyStatus string

// Apply status values for manifest version records.
const (
	ApplyStatusApplied    ApplyStatus = "APPLIED"
	ApplyStatusFailed     ApplyStatus = "FAILED"
	ApplyStatusRolledBack ApplyStatus = "ROLLED_BACK"
	ApplyStatusPartial    ApplyStatus = "PARTIAL"
)

// PhaseExecutionStatus represents the outcome of a single execution phase.
type PhaseExecutionStatus string

// Phase execution status values.
const (
	PhaseStatusPending   PhaseExecutionStatus = "PENDING"
	PhaseStatusRunning   PhaseExecutionStatus = "RUNNING"
	PhaseStatusCompleted PhaseExecutionStatus = "COMPLETED"
	PhaseStatusFailed    PhaseExecutionStatus = "FAILED"
	PhaseStatusSkipped   PhaseExecutionStatus = "SKIPPED"
)

// PhaseStatusEntry records the execution result for a single phase.
type PhaseStatusEntry struct {
	Status      PhaseExecutionStatus `json:"status"`
	StartedAt   *time.Time           `json:"started_at,omitempty"`
	CompletedAt *time.Time           `json:"completed_at,omitempty"`
	Error       string               `json:"error,omitempty"`
}

// PhaseStatusMap is the top-level phase_status JSONB structure, keyed by phase label
// (e.g., "phase_1", "phase_2").
type PhaseStatusMap map[string]PhaseStatusEntry

// VersionEntity represents a row in the manifest_versions table.
type VersionEntity struct {
	ID                uuid.UUID   `gorm:"column:id;type:uuid;primaryKey"`
	Version           string      `gorm:"column:version;type:varchar(50);not null"`
	ManifestJSON      string      `gorm:"column:manifest_json;type:jsonb;not null"`
	AppliedAt         time.Time   `gorm:"column:applied_at;not null"`
	AppliedBy         string      `gorm:"column:applied_by;type:varchar(255);not null"`
	ApplyStatus       ApplyStatus `gorm:"column:apply_status;type:varchar(20);not null"`
	ApplyJobID        *uuid.UUID  `gorm:"column:apply_job_id;type:uuid"`
	DiffSummary       *string     `gorm:"column:diff_summary;type:text"`
	RelationshipGraph *string     `gorm:"column:relationship_graph;type:jsonb"`
	SequenceNumber    int64       `gorm:"column:sequence_number;not null;default:0"`
	Checksum          *string     `gorm:"column:checksum;type:varchar(64)"`
	Source            *string     `gorm:"column:source;type:varchar(20)"`
	ResourcePath      *string     `gorm:"column:resource_path;type:varchar(255)"`
	PhaseStatus       *string     `gorm:"column:phase_status;type:jsonb"`
	CreatedAt         time.Time   `gorm:"column:created_at;not null"`
}

// GetPhaseStatus deserializes the phase_status JSON column into a PhaseStatusMap.
// Returns nil if phase_status is not set.
func (e *VersionEntity) GetPhaseStatus() (PhaseStatusMap, error) {
	if e.PhaseStatus == nil {
		return nil, nil //nolint:nilnil // nil phase_status is a valid empty state
	}
	var m PhaseStatusMap
	if err := json.Unmarshal([]byte(*e.PhaseStatus), &m); err != nil {
		return nil, fmt.Errorf("unmarshal phase_status: %w", err)
	}
	return m, nil
}

// SetPhaseStatus serializes a PhaseStatusMap into the phase_status JSON column.
func (e *VersionEntity) SetPhaseStatus(m PhaseStatusMap) error {
	if m == nil {
		e.PhaseStatus = nil
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal phase_status: %w", err)
	}
	s := string(b)
	e.PhaseStatus = &s
	return nil
}

// TableName returns the table name for GORM.
func (VersionEntity) TableName() string {
	return "manifest_versions"
}

// Repository provides persistence operations for manifest versions.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new manifest version repository.
func NewRepository(database *gorm.DB) (*Repository, error) {
	if database == nil {
		return nil, ErrNilDatabase
	}
	return &Repository{db: database}, nil
}

// Store saves a new manifest version record, atomically assigning the next
// sequence number. The entity's SequenceNumber field is updated in place.
//
// If expectedSeq is non-zero, the current sequence number must match it
// (optimistic locking). A mismatch returns ErrSequenceConflict.
// Pass 0 to skip the check (first apply or overwrite mode).
func (r *Repository) Store(ctx context.Context, entity *VersionEntity, expectedSeq int64) error {
	return db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		// Atomically compute the next sequence number.
		var maxSeq *int64
		if err := tx.Model(&VersionEntity{}).Select("MAX(sequence_number)").Scan(&maxSeq).Error; err != nil {
			return fmt.Errorf("failed to get max sequence number: %w", err)
		}

		var currentSeq int64
		if maxSeq != nil {
			currentSeq = *maxSeq
		}

		// Optimistic locking: verify expected sequence within the same transaction.
		if expectedSeq != 0 && currentSeq != expectedSeq {
			return fmt.Errorf("%w: expected %d but current is %d",
				ErrSequenceConflict, expectedSeq, currentSeq)
		}

		entity.SequenceNumber = currentSeq + 1

		if err := tx.Create(entity).Error; err != nil {
			return fmt.Errorf("failed to store manifest version: %w", err)
		}
		return nil
	})
}

// GetCurrentSequenceNumber returns the highest sequence_number across all
// manifest versions for the tenant, or 0 if no versions exist.
func (r *Repository) GetCurrentSequenceNumber(ctx context.Context) (int64, error) {
	var result int64
	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		var maxSeq *int64
		if err := tx.Model(&VersionEntity{}).Select("MAX(sequence_number)").Scan(&maxSeq).Error; err != nil {
			return fmt.Errorf("failed to get current sequence number: %w", err)
		}
		if maxSeq != nil {
			result = *maxSeq
		}
		return nil
	})
	return result, err
}

// GetLatestApplied retrieves the most recently applied manifest version.
func (r *Repository) GetLatestApplied(ctx context.Context) (*VersionEntity, error) {
	var entity VersionEntity
	var found bool

	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Where("apply_status = ?", ApplyStatusApplied).
			Order("applied_at DESC").
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		found = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrVersionNotFound
	}
	return &entity, nil
}

// GetByVersion retrieves a manifest version by its version string.
// If multiple records share the same version, returns the most recently applied one.
func (r *Repository) GetByVersion(ctx context.Context, version string) (*VersionEntity, error) {
	var entity VersionEntity
	var found bool

	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Where("version = ?", version).
			Order("applied_at DESC").
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		found = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrVersionNotFound
	}
	return &entity, nil
}

// List retrieves a paginated list of manifest versions ordered by applied_at DESC.
func (r *Repository) List(ctx context.Context, limit, offset int) ([]VersionEntity, int, error) {
	var entities []VersionEntity
	var totalCount int64

	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		// Count total records
		if err := tx.Model(&VersionEntity{}).Count(&totalCount).Error; err != nil {
			return fmt.Errorf("failed to count manifest versions: %w", err)
		}

		// Fetch paginated results
		result := tx.Order("applied_at DESC").
			Limit(limit).
			Offset(offset).
			Find(&entities)

		if result.Error != nil {
			return fmt.Errorf("failed to list manifest versions: %w", result.Error)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return entities, int(totalCount), nil
}

// UpdatePhaseStatus updates the phase_status JSONB column for a manifest version.
func (r *Repository) UpdatePhaseStatus(ctx context.Context, id uuid.UUID, phaseStatusJSON *string) error {
	return db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Model(&VersionEntity{}).Where("id = ?", id).Update("phase_status", phaseStatusJSON)
		if result.Error != nil {
			return fmt.Errorf("update phase_status: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrVersionNotFound
		}
		return nil
	})
}

// GetBySequenceNumber retrieves a manifest version by its sequence number.
func (r *Repository) GetBySequenceNumber(ctx context.Context, seq int64) (*VersionEntity, error) {
	var entity VersionEntity
	var found bool

	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Where("sequence_number = ?", seq).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		found = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrVersionNotFound
	}
	return &entity, nil
}

// GetPreviousApplied retrieves the applied manifest version immediately before the given timestamp.
func (r *Repository) GetPreviousApplied(ctx context.Context, beforeTime time.Time) (*VersionEntity, error) {
	var entity VersionEntity
	var found bool

	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Where("apply_status = ? AND applied_at < ?", ApplyStatusApplied, beforeTime).
			Order("applied_at DESC").
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			found = false
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		found = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrVersionNotFound
	}
	return &entity, nil
}
