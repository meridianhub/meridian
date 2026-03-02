package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	"gorm.io/gorm"
)

// InstructionRepository implements ports.InstructionRepository using CockroachDB via GORM.
type InstructionRepository struct {
	db *gorm.DB
}

// NewInstructionRepository creates a new InstructionRepository.
func NewInstructionRepository(db *gorm.DB) *InstructionRepository {
	return &InstructionRepository{db: db}
}

// Save creates or updates an instruction with optimistic locking.
//
// For new instructions (version == 0 on entity), performs an INSERT.
// For existing instructions, performs an UPDATE guarded by the previous version.
// Returns ports.ErrInstructionConflict when a concurrent modification is detected.
// Returns ports.ErrDuplicateIdempotency on unique constraint violation for the idempotency key.
func (r *InstructionRepository) Save(ctx context.Context, inst *domain.Instruction, idempotencyKey string) error {
	entity, err := instructionToEntity(inst, idempotencyKey)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing InstructionEntity
		result := tx.Where("id = ?", entity.ID).First(&existing)

		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// New instruction - INSERT
			entity.Version = 1
			if err := tx.Create(entity).Error; err != nil {
				if isDuplicateKeyError(err) {
					return ports.ErrDuplicateIdempotency
				}
				return err
			}
			return nil
		}

		// Existing instruction - UPDATE with optimistic locking.
		// entity.Version carries the version the caller loaded from the DB (set by instructionFromEntity).
		// We require the DB row still has that version; on match we increment to entity.Version+1.
		expectedVersion := entity.Version
		entity.Version = expectedVersion + 1
		entity.CreatedAt = existing.CreatedAt

		updateResult := tx.Model(&InstructionEntity{}).
			Where("id = ? AND version = ?", entity.ID, expectedVersion).
			Updates(map[string]interface{}{
				"status":         entity.Status,
				"attempt_count":  entity.AttemptCount,
				"next_retry_at":  entity.NextRetryAt,
				"dispatched_at":  entity.DispatchedAt,
				"completed_at":   entity.CompletedAt,
				"failure_reason": entity.FailureReason,
				"error_code":     entity.ErrorCode,
				"version":        entity.Version,
				"updated_at":     entity.UpdatedAt,
			})

		if updateResult.Error != nil {
			return updateResult.Error
		}

		if updateResult.RowsAffected == 0 {
			return ports.ErrInstructionConflict
		}

		return nil
	})
}

// FindByID retrieves an instruction and its attempts by UUID.
func (r *InstructionRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Instruction, error) {
	var entity InstructionEntity
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&entity)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ports.ErrInstructionNotFound
	}
	if result.Error != nil {
		return nil, result.Error
	}

	attempts, err := r.fetchAttempts(ctx, id)
	if err != nil {
		return nil, err
	}

	return instructionFromEntity(&entity, attempts)
}

// FetchDispatchable atomically fetches a batch of PENDING/RETRYING instructions
// ready for dispatch using SELECT FOR UPDATE SKIP LOCKED.
// Instructions are ordered by priority DESC then scheduled_at ASC (matching the outbox index).
func (r *InstructionRepository) FetchDispatchable(ctx context.Context, params ports.FetchDispatchableParams) ([]*domain.Instruction, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}
	asOf := params.AsOf
	if asOf.IsZero() {
		asOf = time.Now()
	}

	var entities []InstructionEntity

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SELECT FOR UPDATE SKIP LOCKED - same pattern as events/outbox.go.
		// Partial index idx_instructions_outbox covers status IN ('PENDING','RETRYING').
		rawSQL := `
            SELECT * FROM instructions
            WHERE status IN ('PENDING', 'RETRYING')
              AND (scheduled_at IS NULL OR scheduled_at <= ?)
            ORDER BY priority DESC, scheduled_at ASC NULLS FIRST
            LIMIT ?
            FOR UPDATE SKIP LOCKED`

		return tx.Raw(rawSQL, asOf, limit).Scan(&entities).Error
	})
	if err != nil {
		return nil, err
	}

	if len(entities) == 0 {
		return nil, nil
	}

	results := make([]*domain.Instruction, 0, len(entities))
	for i := range entities {
		attempts, err := r.fetchAttempts(ctx, entities[i].ID)
		if err != nil {
			return nil, err
		}
		inst, err := instructionFromEntity(&entities[i], attempts)
		if err != nil {
			return nil, err
		}
		results = append(results, inst)
	}

	return results, nil
}

// fetchAttempts loads instruction_attempts for a given instruction ID.
func (r *InstructionRepository) fetchAttempts(ctx context.Context, instructionID uuid.UUID) ([]InstructionAttemptEntity, error) {
	var attempts []InstructionAttemptEntity
	err := r.db.WithContext(ctx).
		Where("instruction_id = ?", instructionID).
		Order("attempt_number ASC").
		Find(&attempts).Error
	return attempts, err
}

// isDuplicateKeyError checks whether err is a unique constraint violation.
// Works for CockroachDB (SQLSTATE 23505) and common error message patterns.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(s, "23505") ||
		strings.Contains(s, "duplicate key") ||
		strings.Contains(s, "unique constraint") ||
		strings.Contains(s, "UniqueViolation")
}
