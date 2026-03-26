package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var _ OutboxRepository = (*PostgresOutboxRepository)(nil)

const (
	hourlyRateLimit    = 500
	rateLimitWindow    = time.Hour
	maxFetchBatchSize  = 100
)

var (
	ErrRateLimitExceeded = errors.New("email: tenant hourly rate limit exceeded")
	ErrOutboxNotFound    = errors.New("email: outbox entry not found")
)

// PostgresOutboxRepository implements OutboxRepository using GORM with
// CockroachDB-compatible queries.
type PostgresOutboxRepository struct {
	db *gorm.DB
}

// NewPostgresOutboxRepository creates a new outbox repository.
func NewPostgresOutboxRepository(gormDB *gorm.DB) *PostgresOutboxRepository {
	return &PostgresOutboxRepository{db: gormDB}
}

func (r *PostgresOutboxRepository) Enqueue(ctx context.Context, entry *OutboxEntry) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}

	templateJSON, err := json.Marshal(entry.TemplateData)
	if err != nil {
		return fmt.Errorf("email: failed to marshal template data: %w", err)
	}

	now := time.Now().UTC()
	entity := OutboxEntity{
		ID:             entry.ID,
		TenantID:       entry.TenantID,
		IdempotencyKey: entry.IdempotencyKey,
		ToAddresses:    entry.ToAddresses,
		FromAddress:    entry.FromAddress,
		Subject:        entry.Subject,
		TemplateName:   entry.TemplateName,
		TemplateData:   datatypes.JSON(templateJSON),
		Status:         string(StatusPending),
		Attempts:       0,
		MaxAttempts:    entry.MaxAttempts,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if entity.MaxAttempts == 0 {
		entity.MaxAttempts = 5
	}

	return db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		// Check hourly rate limit for this tenant
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			return tenant.ErrMissingTenantContext
		}

		var count int64
		windowStart := now.Add(-rateLimitWindow)
		if err := tx.Model(&OutboxEntity{}).
			Where("tenant_id = ? AND created_at >= ?", string(tenantID), windowStart).
			Count(&count).Error; err != nil {
			return fmt.Errorf("email: failed to check rate limit: %w", err)
		}
		if count >= hourlyRateLimit {
			return ErrRateLimitExceeded
		}

		result := tx.Create(&entity)
		if result.Error != nil {
			if isDuplicateKeyError(result.Error) {
				return fmt.Errorf("email: duplicate idempotency key %q: %w", entry.IdempotencyKey, result.Error)
			}
			return result.Error
		}

		entry.CreatedAt = entity.CreatedAt
		entry.UpdatedAt = entity.UpdatedAt
		entry.Status = StatusPending
		return nil
	})
}

func (r *PostgresOutboxRepository) FetchDispatchable(ctx context.Context, limit int) ([]OutboxEntry, error) {
	if limit <= 0 || limit > maxFetchBatchSize {
		limit = maxFetchBatchSize
	}

	var entities []OutboxEntity
	now := time.Now().UTC()

	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		// Atomically select and claim rows by setting status to SENDING.
		// The CTE locks rows with FOR UPDATE SKIP LOCKED, then the UPDATE
		// sets status to SENDING before returning. This prevents concurrent
		// workers from fetching the same entries after the transaction commits.
		return tx.Raw(`
			WITH claimable AS (
				SELECT id FROM email_outbox
				WHERE status IN ('PENDING', 'FAILED')
				AND attempts < max_attempts
				AND next_attempt_at <= ?
				ORDER BY next_attempt_at ASC
				LIMIT ?
				FOR UPDATE SKIP LOCKED
			)
			UPDATE email_outbox
			SET status = 'SENDING', updated_at = ?
			FROM claimable
			WHERE email_outbox.id = claimable.id
			RETURNING email_outbox.*
		`, now, limit, now).Scan(&entities).Error
	})
	if err != nil {
		return nil, fmt.Errorf("email: failed to fetch dispatchable entries: %w", err)
	}

	entries := make([]OutboxEntry, len(entities))
	for i, e := range entities {
		entries[i] = entityToOutboxEntry(e)
	}
	return entries, nil
}

func (r *PostgresOutboxRepository) MarkSent(ctx context.Context, id uuid.UUID) error {
	return db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Model(&OutboxEntity{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"status":     string(StatusSent),
				"updated_at": time.Now().UTC(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrOutboxNotFound
		}
		return nil
	})
}

// retryBackoffs defines the backoff schedule per PRD: 1m, 15m, 1h, 4h, 24h.
// This gives transient provider outages ~29h to resolve before dead-lettering.
var retryBackoffs = []time.Duration{
	1 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	4 * time.Hour,
	24 * time.Hour,
}

func (r *PostgresOutboxRepository) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	return db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		var current OutboxEntity
		if err := tx.Where("id = ?", id).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOutboxNotFound
			}
			return err
		}

		newAttempts := current.Attempts + 1
		now := time.Now().UTC()

		// Dead-letter if all attempts exhausted
		if newAttempts >= current.MaxAttempts {
			return tx.Model(&OutboxEntity{}).
				Where("id = ?", id).
				Updates(map[string]any{
					"status":     string(StatusDeadLetter),
					"attempts":   newAttempts,
					"last_error": errMsg,
					"updated_at": now,
				}).Error
		}

		// Use PRD backoff schedule, falling back to last interval for extra attempts
		backoffIdx := current.Attempts
		if backoffIdx >= len(retryBackoffs) {
			backoffIdx = len(retryBackoffs) - 1
		}
		nextAttempt := now.Add(retryBackoffs[backoffIdx])

		return tx.Model(&OutboxEntity{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"status":          string(StatusFailed),
				"attempts":        newAttempts,
				"last_error":      errMsg,
				"next_attempt_at": nextAttempt,
				"updated_at":      now,
			}).Error
	})
}

func (r *PostgresOutboxRepository) Cancel(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	return db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		result := tx.Model(&OutboxEntity{}).
			Where("id = ? AND status IN ('PENDING', 'SENDING', 'FAILED')", id).
			Updates(map[string]any{
				"status":       string(StatusCancelled),
				"cancelled_at": now,
				"updated_at":   now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrOutboxNotFound
		}
		return nil
	})
}

func entityToOutboxEntry(e OutboxEntity) OutboxEntry {
	var templateData map[string]any
	if len(e.TemplateData) > 0 {
		_ = json.Unmarshal(e.TemplateData, &templateData)
	}

	return OutboxEntry{
		ID:             e.ID,
		TenantID:       e.TenantID,
		IdempotencyKey: e.IdempotencyKey,
		ToAddresses:    []string(e.ToAddresses),
		FromAddress:    e.FromAddress,
		Subject:        e.Subject,
		TemplateName:   e.TemplateName,
		TemplateData:   templateData,
		Status:         OutboxStatus(e.Status),
		Attempts:       e.Attempts,
		MaxAttempts:    e.MaxAttempts,
		NextAttemptAt:  e.NextAttemptAt,
		LastError:      e.LastError,
		CancelledAt:    e.CancelledAt,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
}

func isDuplicateKeyError(err error) bool {
	return strings.Contains(err.Error(), "duplicate key") ||
		strings.Contains(err.Error(), "SQLSTATE 23505")
}
