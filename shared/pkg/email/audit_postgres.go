package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var _ AuditRepository = (*PostgresAuditRepository)(nil)

// PostgresAuditRepository implements AuditRepository using GORM.
type PostgresAuditRepository struct {
	db *gorm.DB
}

// NewPostgresAuditRepository creates a new audit repository.
func NewPostgresAuditRepository(gormDB *gorm.DB) *PostgresAuditRepository {
	return &PostgresAuditRepository{db: gormDB}
}

// Record persists a new audit log entry within a tenant-scoped transaction.
func (r *PostgresAuditRepository) Record(ctx context.Context, entry *AuditEntry) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}

	var providerResponseJSON datatypes.JSON
	if entry.ProviderResponse != nil {
		data, err := json.Marshal(entry.ProviderResponse)
		if err != nil {
			return fmt.Errorf("email: failed to marshal provider response: %w", err)
		}
		providerResponseJSON = datatypes.JSON(data)
	}

	now := time.Now().UTC()

	return db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			return tenant.ErrMissingTenantContext
		}

		entity := AuditLogEntity{
			ID:               entry.ID,
			TenantID:         string(tenantID),
			OutboxID:         entry.OutboxID,
			ProviderID:       entry.ProviderID,
			ToAddresses:      entry.ToAddresses,
			FromAddress:      entry.FromAddress,
			Subject:          entry.Subject,
			TemplateName:     entry.TemplateName,
			Status:           string(entry.Status),
			SentAt:           entry.SentAt,
			DeliveredAt:      entry.DeliveredAt,
			BounceReason:     entry.BounceReason,
			ProviderResponse: providerResponseJSON,
			CreatedAt:        now,
		}

		return tx.Create(&entity).Error
	})
}

// FindByOutboxID returns all audit entries for the given outbox ID, newest first.
func (r *PostgresAuditRepository) FindByOutboxID(ctx context.Context, outboxID uuid.UUID) ([]AuditEntry, error) {
	var entities []AuditLogEntity

	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		return tx.Where("outbox_id = ?", outboxID).
			Order("created_at DESC").
			Find(&entities).Error
	})
	if err != nil {
		return nil, fmt.Errorf("email: failed to find audit entries: %w", err)
	}

	entries := make([]AuditEntry, len(entities))
	for i, e := range entities {
		entries[i] = entityToAuditEntry(e)
	}
	return entries, nil
}

// FindByProviderID returns all audit entries for a given provider ID (cross-tenant).
func (r *PostgresAuditRepository) FindByProviderID(ctx context.Context, providerID string) ([]AuditEntry, error) {
	var entities []AuditLogEntity

	if err := r.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("created_at DESC").
		Find(&entities).Error; err != nil {
		return nil, fmt.Errorf("email: failed to find audit entries by provider ID: %w", err)
	}

	entries := make([]AuditEntry, len(entities))
	for i, e := range entities {
		entries[i] = entityToAuditEntry(e)
	}
	return entries, nil
}

// RecordByProviderID looks up existing audit entries by providerID (without tenant scope),
// resolves the tenant from the first match, then records a new status update entry.
func (r *PostgresAuditRepository) RecordByProviderID(ctx context.Context, providerID string, status AuditStatus, payload map[string]any) error {
	// Cross-tenant lookup: find any existing audit entry with this provider ID.
	var existing AuditLogEntity
	if err := r.db.Where("provider_id = ?", providerID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrAuditEntryNotFound
		}
		return fmt.Errorf("email: lookup audit entry by provider ID: %w", err)
	}

	tid, err := tenant.NewTenantID(existing.TenantID)
	if err != nil {
		return fmt.Errorf("email: invalid tenant ID in audit entry: %w", err)
	}
	tenantCtx := tenant.WithTenant(ctx, tid)

	now := time.Now().UTC()

	var delivered *time.Time
	var bounceReason *string
	if status == AuditStatusDelivered {
		delivered = &now
	}

	entry := &AuditEntry{
		ID:               uuid.New(),
		OutboxID:         existing.OutboxID,
		ProviderID:       &providerID,
		ToAddresses:      []string(existing.ToAddresses),
		FromAddress:      existing.FromAddress,
		Subject:          existing.Subject,
		TemplateName:     existing.TemplateName,
		Status:           status,
		DeliveredAt:      delivered,
		BounceReason:     bounceReason,
		ProviderResponse: payload,
	}

	return r.Record(tenantCtx, entry)
}

func entityToAuditEntry(e AuditLogEntity) AuditEntry {
	var providerResponse map[string]any
	if len(e.ProviderResponse) > 0 {
		_ = json.Unmarshal(e.ProviderResponse, &providerResponse)
	}

	return AuditEntry{
		ID:               e.ID,
		TenantID:         e.TenantID,
		OutboxID:         e.OutboxID,
		ProviderID:       e.ProviderID,
		ToAddresses:      []string(e.ToAddresses),
		FromAddress:      e.FromAddress,
		Subject:          e.Subject,
		TemplateName:     e.TemplateName,
		Status:           AuditStatus(e.Status),
		SentAt:           e.SentAt,
		DeliveredAt:      e.DeliveredAt,
		BounceReason:     e.BounceReason,
		ProviderResponse: providerResponse,
		CreatedAt:        e.CreatedAt,
	}
}
