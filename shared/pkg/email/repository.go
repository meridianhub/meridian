package email

import (
	"context"
	"errors"
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

	// FindByProviderID returns all audit entries for a given provider-assigned
	// email ID (cross-tenant lookup). Returns entries ordered newest-first.
	FindByProviderID(ctx context.Context, providerID string) ([]AuditEntry, error)

	// RecordByProviderID records a webhook delivery status update for the email
	// identified by providerID. It looks up the tenant from existing audit entries
	// and records a new status entry with the supplied payload. Returns
	// ErrAuditEntryNotFound if no audit entry with the given providerID exists.
	RecordByProviderID(ctx context.Context, providerID string, status AuditStatus, payload map[string]any) error
}

// ErrAuditEntryNotFound is returned when no audit entry matches the given criteria.
var ErrAuditEntryNotFound = fmt.Errorf("email: audit entry not found")

// CompositeAuditRepository fans out read operations across multiple underlying
// repositories. This is used by the webhook handler which must resolve provider
// IDs across all service databases (payment-order, identity, current-account).
// Write operations (Record) are not supported and will panic - use the
// service-specific repository for writes.
type CompositeAuditRepository struct {
	repos []AuditRepository
}

// NewCompositeAuditRepository creates a repository that searches across all
// provided repositories for read operations.
func NewCompositeAuditRepository(repos ...AuditRepository) *CompositeAuditRepository {
	return &CompositeAuditRepository{repos: repos}
}

// Record is not supported on composite repositories. The webhook handler only
// reads and records by provider ID; direct writes go to service-specific repos.
func (c *CompositeAuditRepository) Record(_ context.Context, _ *AuditEntry) error {
	panic("CompositeAuditRepository does not support Record - use service-specific repository")
}

// FindByOutboxID is not supported on composite repositories.
func (c *CompositeAuditRepository) FindByOutboxID(_ context.Context, _ uuid.UUID) ([]AuditEntry, error) {
	panic("CompositeAuditRepository does not support FindByOutboxID - use service-specific repository")
}

// FindByProviderID searches all underlying repositories for audit entries
// matching the provider ID. Returns results from the first repository that
// finds entries.
func (c *CompositeAuditRepository) FindByProviderID(ctx context.Context, providerID string) ([]AuditEntry, error) {
	for _, repo := range c.repos {
		entries, err := repo.FindByProviderID(ctx, providerID)
		if err != nil {
			continue
		}
		if len(entries) > 0 {
			return entries, nil
		}
	}
	return nil, nil
}

// RecordByProviderID searches all underlying repositories for an existing audit
// entry matching the provider ID and records the status update in the same
// repository.
func (c *CompositeAuditRepository) RecordByProviderID(ctx context.Context, providerID string, status AuditStatus, payload map[string]any) error {
	for _, repo := range c.repos {
		err := repo.RecordByProviderID(ctx, providerID, status, payload)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrAuditEntryNotFound) {
			return err
		}
	}
	return ErrAuditEntryNotFound
}
