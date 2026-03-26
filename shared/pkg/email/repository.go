package email

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// OutboxRepository manages email outbox entries for reliable delivery.
type OutboxRepository interface {
	// Enqueue adds a new email to the outbox for delivery.
	Enqueue(ctx context.Context, entry *OutboxEntry) error

	// FetchDispatchable returns up to limit entries ready for delivery,
	// locking them to prevent concurrent processing.
	FetchDispatchable(ctx context.Context, limit int) ([]OutboxEntry, error)

	// MarkSent marks an outbox entry as successfully sent.
	MarkSent(ctx context.Context, id uuid.UUID) error

	// MarkFailed records a failed delivery attempt with backoff.
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error

	// Cancel marks an outbox entry as cancelled.
	Cancel(ctx context.Context, id uuid.UUID) error

	// CancelByIdempotencyKeyPattern cancels all pending/failed outbox entries
	// whose idempotency key matches the given SQL LIKE pattern.
	// Returns the number of entries cancelled.
	CancelByIdempotencyKeyPattern(ctx context.Context, pattern string) (int64, error)
}

// AuditRepository records email delivery events for compliance and debugging.
type AuditRepository interface {
	// Record persists an audit entry.
	Record(ctx context.Context, entry *AuditEntry) error

	// FindByOutboxID returns all audit entries for a given outbox entry.
	FindByOutboxID(ctx context.Context, outboxID uuid.UUID) ([]AuditEntry, error)

	// RecordByProviderID records a webhook delivery status update for the email
	// identified by providerID. It looks up the tenant from existing audit entries
	// and records a new status entry with the supplied payload. Returns
	// ErrAuditEntryNotFound if no audit entry with the given providerID exists.
	RecordByProviderID(ctx context.Context, providerID string, status AuditStatus, payload map[string]any) error
}

// ErrAuditEntryNotFound is returned when no audit entry matches the given criteria.
var ErrAuditEntryNotFound = fmt.Errorf("email: audit entry not found")
