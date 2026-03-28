package persistence

import (
	"context"
	"database/sql"
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

// WithTx returns a new InstructionRepository that operates within the provided transaction.
// Use this to perform repository operations within a caller-managed transaction, enabling
// atomic commits with other operations (e.g., outbox event writes).
func (r *InstructionRepository) WithTx(tx *gorm.DB) *InstructionRepository {
	return &InstructionRepository{db: tx}
}

// Save creates or updates an instruction with optimistic locking.
//
// For new instructions (Version == 0), performs an INSERT and sets inst.Version = 1.
// For existing instructions, performs an UPDATE guarded by the caller's Version.
// On success, inst.Version is updated to the new DB value so subsequent Saves work correctly.
// Returns ports.ErrInstructionConflict when a concurrent modification is detected.
// Returns ports.ErrDuplicateIdempotency on unique constraint violation for the idempotency key.
func (r *InstructionRepository) Save(ctx context.Context, inst *domain.Instruction, idempotencyKey string) error {
	entity, err := instructionToEntity(inst, idempotencyKey)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.saveInTx(ctx, tx, inst, entity)
	})
}

// SaveInTx performs the save operation within the provided transaction.
// This is used when the caller manages the transaction (e.g., for atomic event publishing).
func (r *InstructionRepository) SaveInTx(ctx context.Context, tx *gorm.DB, inst *domain.Instruction, idempotencyKey string) error {
	entity, err := instructionToEntity(inst, idempotencyKey)
	if err != nil {
		return err
	}
	return r.saveInTx(ctx, tx, inst, entity)
}

// saveInTx contains the core save logic operating on a provided transaction handle.
func (r *InstructionRepository) saveInTx(ctx context.Context, tx *gorm.DB, inst *domain.Instruction, entity *InstructionEntity) error {
	var existing InstructionEntity
	result := tx.WithContext(ctx).Where("id = ?", entity.ID).First(&existing)

	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return result.Error
	}

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// New instruction - INSERT
		entity.Version = 1
		if err := tx.WithContext(ctx).Create(entity).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ports.ErrDuplicateIdempotency
			}
			return err
		}
		// Propagate the assigned version back so the caller doesn't hold a stale 0.
		inst.Version = entity.Version
		return nil
	}

	// Existing instruction - UPDATE with optimistic locking.
	// entity.Version carries the version the caller loaded from the DB (set by instructionFromEntity).
	// We require the DB row still has that version; on match we increment to entity.Version+1.
	expectedVersion := entity.Version
	newVersion := expectedVersion + 1
	entity.Version = newVersion
	entity.CreatedAt = existing.CreatedAt

	updateResult := tx.WithContext(ctx).Model(&InstructionEntity{}).
		Where("id = ? AND version = ?", entity.ID, expectedVersion).
		Updates(map[string]interface{}{
			"status":         entity.Status,
			"attempt_count":  entity.AttemptCount,
			"next_retry_at":  entity.NextRetryAt,
			"dispatched_at":  entity.DispatchedAt,
			"completed_at":   entity.CompletedAt,
			"failure_reason": entity.FailureReason,
			"error_code":     entity.ErrorCode,
			"version":        newVersion,
			"updated_at":     entity.UpdatedAt,
		})

	if updateResult.Error != nil {
		return updateResult.Error
	}

	if updateResult.RowsAffected == 0 {
		return ports.ErrInstructionConflict
	}

	// Propagate the new version back so the caller can make further Saves without reloading.
	inst.Version = newVersion
	return nil
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

// FetchDispatchable atomically claims a batch of PENDING/RETRYING instructions ready for
// dispatch. Within a single transaction it:
//  1. Locks candidate rows with SELECT FOR UPDATE SKIP LOCKED so concurrent workers skip them.
//  2. Marks them DISPATCHING and increments attempt_count and version within the same tx.
//  3. Returns the updated rows to the caller.
//
// PENDING instructions are gated on scheduled_at.
// RETRYING instructions are additionally gated on next_retry_at.
// Instructions are evaluated in priority DESC, scheduled_at ASC order.
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

	// Use READ COMMITTED isolation: CockroachDB's SERIALIZABLE default causes
	// unpredictable behavior with FOR UPDATE SKIP LOCKED, where recently-committed
	// rows may be skipped. READ COMMITTED matches PostgreSQL semantics and is the
	// recommended isolation level for queue-like claim patterns.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.claimDispatchable(tx, asOf, limit, &entities)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}

	return r.entitiesToInstructions(ctx, entities)
}

// claimDispatchable locks candidate instructions and marks them DISPATCHING within a transaction.
func (r *InstructionRepository) claimDispatchable(tx *gorm.DB, asOf time.Time, limit int, entities *[]InstructionEntity) error {
	lockSQL := `
            SELECT id FROM instructions
            WHERE (
                (status = 'PENDING' AND (scheduled_at IS NULL OR scheduled_at <= ?))
                OR
                (status = 'RETRYING' AND (next_retry_at IS NULL OR next_retry_at <= ?))
            )
            ORDER BY priority DESC, scheduled_at ASC NULLS FIRST
            LIMIT ?
            FOR UPDATE SKIP LOCKED`

	var ids []uuid.UUID
	if err := tx.Raw(lockSQL, asOf, asOf, limit).Scan(&ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	if err := tx.Model(&InstructionEntity{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"status":        string(domain.InstructionStatusDispatching),
			"attempt_count": gorm.Expr("attempt_count + 1"),
			"dispatched_at": asOf,
			"updated_at":    asOf,
			"version":       gorm.Expr("version + 1"),
		}).Error; err != nil {
		return err
	}

	return tx.Where("id IN ?", ids).
		Order("priority DESC, scheduled_at ASC NULLS FIRST").
		Find(entities).Error
}

// entitiesToInstructions converts a slice of entities to domain instructions, loading attempts.
func (r *InstructionRepository) entitiesToInstructions(ctx context.Context, entities []InstructionEntity) ([]*domain.Instruction, error) {
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

// ListByTenant retrieves instructions for a tenant with optional filtering and pagination.
// Returns the matching instructions and the total count matching the filter (before pagination).
// Results are ordered by created_at DESC, id DESC for stable cursor-based pagination.
func (r *InstructionRepository) ListByTenant(ctx context.Context, params ports.ListInstructionsParams) ([]*domain.Instruction, int64, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	query := r.db.WithContext(ctx).Model(&InstructionEntity{}).
		Where("tenant_id = ?", params.TenantID)

	if params.InstructionType != "" {
		query = query.Where("instruction_type = ?", params.InstructionType)
	}
	if params.ProviderConnectionID != "" {
		query = query.Where("provider_connection_id = ?", params.ProviderConnectionID)
	}
	if len(params.Statuses) > 0 {
		statusStrs := make([]string, len(params.Statuses))
		for i, s := range params.Statuses {
			statusStrs[i] = string(s)
		}
		query = query.Where("status IN ?", statusStrs)
	}
	if !params.CreatedAfter.IsZero() {
		query = query.Where("created_at >= ?", params.CreatedAfter)
	}
	if !params.CreatedBefore.IsZero() {
		query = query.Where("created_at <= ?", params.CreatedBefore)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var entities []InstructionEntity
	if err := query.
		Order("created_at DESC, id DESC").
		Limit(limit).
		Offset(params.Offset).
		Find(&entities).Error; err != nil {
		return nil, 0, err
	}

	results := make([]*domain.Instruction, 0, len(entities))
	for i := range entities {
		attempts, err := r.fetchAttempts(ctx, entities[i].ID)
		if err != nil {
			return nil, 0, err
		}
		inst, err := instructionFromEntity(&entities[i], attempts)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, inst)
	}

	return results, total, nil
}

// FindExpired returns up to batchSize instructions whose expires_at has passed and whose
// status is PENDING or RETRYING. Results are ordered by expires_at ASC so the oldest
// expirations are processed first.
func (r *InstructionRepository) FindExpired(ctx context.Context, batchSize int) ([]*domain.Instruction, error) {
	if batchSize <= 0 {
		batchSize = 100
	}

	var entities []InstructionEntity
	err := r.db.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at < ? AND status IN ?", gorm.Expr("NOW()"), []string{
			string(domain.InstructionStatusPending),
			string(domain.InstructionStatusRetrying),
		}).
		Order("expires_at ASC").
		Limit(batchSize).
		Find(&entities).Error
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
