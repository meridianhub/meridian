package webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DeliveryEntity is the database entity for webhook deliveries.
type DeliveryEntity struct {
	ID            uuid.UUID  `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	EventID       string     `gorm:"column:event_id;not null"`
	EventType     string     `gorm:"column:event_type;not null"`
	TenantID      string     `gorm:"column:tenant_id;not null"`
	AccountID     string     `gorm:"column:account_id;not null"`
	WebhookURL    string     `gorm:"column:webhook_url;not null"`
	Status        string     `gorm:"column:status;not null;default:pending"`
	Attempts      int        `gorm:"column:attempts;not null;default:0"`
	LastAttemptAt *time.Time `gorm:"column:last_attempt_at"`
	LastError     *string    `gorm:"column:last_error"`
	ResponseCode  *int       `gorm:"column:response_code"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	CompletedAt   *time.Time `gorm:"column:completed_at"`
}

// TableName returns the table name for GORM.
func (DeliveryEntity) TableName() string {
	return "webhook_deliveries"
}

// Repository provides database operations for webhook deliveries.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new webhook delivery repository.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// RecordDelivery creates or updates a delivery record.
// Implements the DeliveryRecorder interface.
func (r *Repository) RecordDelivery(ctx context.Context, record *DeliveryRecord) error {
	entity := toEntity(record)

	// Use Upsert pattern: update if exists, insert if not
	result := r.db.WithContext(ctx).Save(entity)
	if result.Error != nil {
		return fmt.Errorf("failed to save webhook delivery: %w", result.Error)
	}

	return nil
}

// GetByID retrieves a delivery record by ID.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*DeliveryRecord, error) {
	var entity DeliveryEntity
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&entity)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get webhook delivery: %w", result.Error)
	}
	return toDomain(&entity), nil
}

// ListByTenant retrieves delivery records for a tenant.
func (r *Repository) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*DeliveryRecord, error) {
	var entities []DeliveryEntity
	result := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Limit(limit).
		Find(&entities)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list webhook deliveries: %w", result.Error)
	}

	records := make([]*DeliveryRecord, len(entities))
	for i := range entities {
		records[i] = toDomain(&entities[i])
	}
	return records, nil
}

// ListByAccount retrieves delivery records for an account.
func (r *Repository) ListByAccount(ctx context.Context, accountID string, limit int) ([]*DeliveryRecord, error) {
	var entities []DeliveryEntity
	result := r.db.WithContext(ctx).
		Where("account_id = ?", accountID).
		Order("created_at DESC").
		Limit(limit).
		Find(&entities)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list webhook deliveries: %w", result.Error)
	}

	records := make([]*DeliveryRecord, len(entities))
	for i := range entities {
		records[i] = toDomain(&entities[i])
	}
	return records, nil
}

// CountByStatus counts deliveries by status for a tenant.
func (r *Repository) CountByStatus(ctx context.Context, tenantID string, status DeliveryStatus) (int64, error) {
	var count int64
	result := r.db.WithContext(ctx).
		Model(&DeliveryEntity{}).
		Where("tenant_id = ? AND status = ?", tenantID, string(status)).
		Count(&count)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to count webhook deliveries: %w", result.Error)
	}
	return count, nil
}

// toEntity converts a domain record to a database entity.
func toEntity(record *DeliveryRecord) *DeliveryEntity {
	entity := &DeliveryEntity{
		ID:            record.ID,
		EventID:       record.EventID,
		EventType:     string(record.EventType),
		TenantID:      record.TenantID,
		AccountID:     record.AccountID,
		WebhookURL:    record.WebhookURL,
		Status:        string(record.Status),
		Attempts:      record.Attempts,
		LastError:     record.LastError,
		ResponseCode:  record.ResponseCode,
		CreatedAt:     record.CreatedAt,
		LastAttemptAt: record.LastAttemptAt,
		CompletedAt:   record.CompletedAt,
	}

	return entity
}

// toDomain converts a database entity to a domain record.
func toDomain(entity *DeliveryEntity) *DeliveryRecord {
	return &DeliveryRecord{
		ID:            entity.ID,
		EventID:       entity.EventID,
		EventType:     EventType(entity.EventType),
		TenantID:      entity.TenantID,
		AccountID:     entity.AccountID,
		WebhookURL:    entity.WebhookURL,
		Status:        DeliveryStatus(entity.Status),
		Attempts:      entity.Attempts,
		LastError:     entity.LastError,
		ResponseCode:  entity.ResponseCode,
		CreatedAt:     entity.CreatedAt,
		LastAttemptAt: entity.LastAttemptAt,
		CompletedAt:   entity.CompletedAt,
	}
}
