// Package email provides the core types and interfaces for transactional email
// delivery in Meridian. It implements an outbox pattern for reliable, at-least-once
// email delivery with audit logging.
package email

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Sender defines the interface for email delivery providers (e.g., SES, SendGrid).
type Sender interface {
	// Send delivers an email message and returns the provider-assigned message ID.
	Send(ctx context.Context, msg Message) (SendResult, error)
}

// Message represents a fully rendered email ready for delivery.
type Message struct {
	To          []string
	From        string
	Subject     string
	HTMLBody    string
	TextBody    string
	ReplyTo     string
	Headers     map[string]string
	Attachments []Attachment
}

// Attachment represents a file attached to an email.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// SendResult contains the outcome of a successful email send.
type SendResult struct {
	ProviderID string
	SentAt     time.Time
}

// OutboxEntry represents a queued email in the outbox table.
type OutboxEntry struct {
	ID             uuid.UUID
	TenantID       string
	IdempotencyKey string
	ToAddresses    []string
	FromAddress    string
	Subject        string
	TemplateName   string
	TemplateData   map[string]any
	Status         OutboxStatus
	Attempts       int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LastError      *string
	CancelledAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// OutboxStatus represents the lifecycle state of an outbox entry.
type OutboxStatus string

// Outbox lifecycle statuses.
const (
	StatusPending    OutboxStatus = "PENDING"
	StatusSending    OutboxStatus = "SENDING"
	StatusSent       OutboxStatus = "SENT"
	StatusFailed     OutboxStatus = "FAILED"
	StatusDeadLetter OutboxStatus = "DEAD_LETTER"
	StatusCancelled  OutboxStatus = "CANCELLED"
)

// AuditStatus represents the delivery state tracked in audit logs.
type AuditStatus string

// Audit log delivery statuses.
const (
	AuditStatusSent      AuditStatus = "SENT"
	AuditStatusDelivered AuditStatus = "DELIVERED"
	AuditStatusBounced   AuditStatus = "BOUNCED"
	AuditStatusFailed    AuditStatus = "FAILED"
)

// AuditEntry represents a record in the email audit log.
type AuditEntry struct {
	ID               uuid.UUID
	TenantID         string
	OutboxID         uuid.UUID
	ProviderID       *string
	ToAddresses      []string
	FromAddress      string
	Subject          string
	TemplateName     string
	Status           AuditStatus
	SentAt           *time.Time
	DeliveredAt      *time.Time
	BounceReason     *string
	ProviderResponse map[string]any
	CreatedAt        time.Time
}
