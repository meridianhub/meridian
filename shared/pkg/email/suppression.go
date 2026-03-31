package email

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SuppressionType indicates why an address was suppressed.
type SuppressionType string

// Suppression type constants.
const (
	SuppressionBounce    SuppressionType = "BOUNCE"
	SuppressionComplaint SuppressionType = "COMPLAINT"
)

// SuppressionEntry represents a suppressed email address.
type SuppressionEntry struct {
	EmailAddress    string
	SuppressionType SuppressionType
	ProviderID      string
	Reason          string
	TenantID        string
}

// SuppressionRepository checks and records email address suppressions.
type SuppressionRepository interface {
	// IsSuppressed returns true if the email address is suppressed for any tenant.
	IsSuppressed(ctx context.Context, emailAddress string) (bool, error)

	// AddSuppression records a suppression entry. Uses ON CONFLICT to avoid duplicates.
	AddSuppression(ctx context.Context, entry *SuppressionEntry) error
}

// SuppressedAddressEntity is the GORM model for the suppressed_addresses table.
type SuppressedAddressEntity struct {
	ID              uuid.UUID `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID        string    `gorm:"not null;uniqueIndex:uq_suppressed_addresses_tenant_email,priority:1"`
	EmailAddress    string    `gorm:"not null;size:255;uniqueIndex:uq_suppressed_addresses_tenant_email,priority:2;index:idx_suppressed_addresses_email"`
	SuppressionType string    `gorm:"not null;size:20"`
	ProviderID      *string   `gorm:"size:255"`
	Reason          *string   `gorm:"type:text"`
	SuppressedAt    time.Time `gorm:"not null"`
	CreatedAt       time.Time `gorm:"not null"`
}

// TableName returns the database table name.
func (SuppressedAddressEntity) TableName() string {
	return "suppressed_addresses"
}

// Suppression validation errors.
var (
	ErrNilSuppressionEntry      = errors.New("email: suppression entry must not be nil")
	ErrEmptySuppressionEmail    = errors.New("email: suppression entry must have an email address")
	ErrEmptySuppressionTenantID = errors.New("email: suppression entry must have a tenant ID")
	ErrInvalidSuppressionType   = errors.New("email: suppression entry must have a valid suppression type")
)

// validSuppressionTypes enumerates accepted SuppressionType values.
var validSuppressionTypes = map[SuppressionType]bool{
	SuppressionBounce:    true,
	SuppressionComplaint: true,
}

var _ SuppressionRepository = (*PostgresSuppressionRepository)(nil)

// PostgresSuppressionRepository implements SuppressionRepository using GORM.
type PostgresSuppressionRepository struct {
	db *gorm.DB
}

// NewPostgresSuppressionRepository creates a new suppression repository.
func NewPostgresSuppressionRepository(gormDB *gorm.DB) *PostgresSuppressionRepository {
	return &PostgresSuppressionRepository{db: gormDB}
}

// IsSuppressed checks whether the given email address is suppressed (cross-tenant).
func (r *PostgresSuppressionRepository) IsSuppressed(ctx context.Context, emailAddress string) (bool, error) {
	normalised := strings.ToLower(strings.TrimSpace(emailAddress))
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&SuppressedAddressEntity{}).
		Where("email_address = ?", normalised).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("email: checking suppression: %w", err)
	}
	return count > 0, nil
}

// AddSuppression records a suppressed address. If the tenant+email combination
// already exists, the row is updated with the latest suppression details.
func (r *PostgresSuppressionRepository) AddSuppression(ctx context.Context, entry *SuppressionEntry) error {
	if entry == nil {
		return ErrNilSuppressionEntry
	}

	normalised := strings.ToLower(strings.TrimSpace(entry.EmailAddress))
	if normalised == "" {
		return ErrEmptySuppressionEmail
	}
	if strings.TrimSpace(entry.TenantID) == "" {
		return ErrEmptySuppressionTenantID
	}
	if !validSuppressionTypes[entry.SuppressionType] {
		return ErrInvalidSuppressionType
	}

	now := time.Now().UTC()

	var providerID *string
	if entry.ProviderID != "" {
		providerID = &entry.ProviderID
	}
	var reason *string
	if entry.Reason != "" {
		reason = &entry.Reason
	}

	entity := SuppressedAddressEntity{
		ID:              uuid.New(),
		TenantID:        entry.TenantID,
		EmailAddress:    normalised,
		SuppressionType: string(entry.SuppressionType),
		ProviderID:      providerID,
		Reason:          reason,
		SuppressedAt:    now,
		CreatedAt:       now,
	}

	// ON CONFLICT update with latest suppression info.
	result := r.db.WithContext(ctx).Exec(`
		INSERT INTO suppressed_addresses (id, tenant_id, email_address, suppression_type, provider_id, reason, suppressed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, email_address)
		DO UPDATE SET suppression_type = EXCLUDED.suppression_type,
		             provider_id = EXCLUDED.provider_id,
		             reason = EXCLUDED.reason,
		             suppressed_at = EXCLUDED.suppressed_at
	`, entity.ID, entity.TenantID, entity.EmailAddress, entity.SuppressionType,
		entity.ProviderID, entity.Reason, entity.SuppressedAt, entity.CreatedAt)

	if result.Error != nil {
		return fmt.Errorf("email: adding suppression: %w", result.Error)
	}
	return nil
}
