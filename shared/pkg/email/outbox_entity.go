package email

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/datatypes"
)

// OutboxEntity is the GORM model for the email_outbox table.
type OutboxEntity struct {
	ID             uuid.UUID      `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID       string         `gorm:"not null;uniqueIndex:idx_email_outbox_tenant_idem,priority:1"`
	IdempotencyKey string         `gorm:"not null;size:255;uniqueIndex:idx_email_outbox_tenant_idem,priority:2"`
	ToAddresses    pq.StringArray `gorm:"type:text[];not null"`
	FromAddress    string         `gorm:"not null;size:255;default:noreply@meridianhub.cloud"`
	Subject        string         `gorm:"not null;size:500"`
	TemplateName   string         `gorm:"not null;size:100"`
	TemplateData   datatypes.JSON `gorm:"not null;default:'{}'"`
	Category       string         `gorm:"size:20;default:TRANSACTIONAL"`
	PartyID        string         `gorm:"size:255"`
	Status         string         `gorm:"not null;size:20;default:PENDING"`
	Attempts       int            `gorm:"not null;default:0"`
	MaxAttempts    int            `gorm:"not null;default:5"`
	NextAttemptAt  time.Time      `gorm:"not null"`
	LastError      *string        `gorm:"type:text"`
	CancelledAt    *time.Time
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

// TableName returns the database table name for OutboxEntity.
func (OutboxEntity) TableName() string {
	return "email_outbox"
}

// AuditLogEntity is the GORM model for the email_audit_log table.
type AuditLogEntity struct {
	ID               uuid.UUID      `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID         string         `gorm:"not null"`
	OutboxID         uuid.UUID      `gorm:"not null;type:uuid"`
	ProviderID       *string        `gorm:"size:255"`
	ToAddresses      pq.StringArray `gorm:"type:text[];not null"`
	FromAddress      string         `gorm:"not null;size:255"`
	Subject          string         `gorm:"not null;size:500"`
	TemplateName     string         `gorm:"not null;size:100"`
	Status           string         `gorm:"not null;size:20;default:SENT"`
	SentAt           *time.Time
	DeliveredAt      *time.Time
	BounceReason     *string        `gorm:"type:text"`
	ProviderResponse datatypes.JSON `gorm:"type:jsonb"`
	CreatedAt        time.Time      `gorm:"not null"`
}

// TableName returns the database table name for AuditLogEntity.
func (AuditLogEntity) TableName() string {
	return "email_audit_log"
}
