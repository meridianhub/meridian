// Package adapters provides concrete implementations of the eventstream ports.
//
// ADR-0002 constraint: The OutboxEventSource adapter is for dev/CI mode only.
// Cross-service database access is forbidden in production. This adapter reads
// event_outbox tables from a shared CockroachDB instance, which is only available
// in local/dev/CI environments where all services share a single database.
// Production deployments MUST use the KafkaEventSource adapter instead.
package adapters

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/gateway/eventstream"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
)

const (
	// DefaultPollInterval is the default interval between outbox polls.
	DefaultPollInterval = 500 * time.Millisecond

	// DefaultBatchSize is the default maximum number of entries fetched per poll.
	DefaultBatchSize = 100
)

// OutboxEventSource polls the event_outbox table for completed entries and
// delivers them to the event streaming pipeline. It implements EventSource
// using high-water mark tracking per service to avoid duplicate delivery.
//
// This adapter is intended for dev/CI mode only (KAFKA_ENABLED=false).
// See ADR-0002 for the constraint on cross-service database access.
type OutboxEventSource struct {
	db           *gorm.DB
	pollInterval time.Duration
	batchSize    int
	logger       *slog.Logger

	mu            sync.Mutex
	highWaterMark map[string]waterMark // service -> last seen position
}

// waterMark tracks the last processed position for a service's outbox entries.
// Using both time and ID avoids reprocessing when multiple entries share the
// same created_at timestamp.
type waterMark struct {
	createdAt time.Time
	id        uuid.UUID
}

// NewOutboxEventSource constructs an OutboxEventSource with the given database
// connection and options. The logger must not be nil.
func NewOutboxEventSource(
	db *gorm.DB,
	pollInterval time.Duration,
	logger *slog.Logger,
) *OutboxEventSource {
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	return &OutboxEventSource{
		db:            db,
		pollInterval:  pollInterval,
		batchSize:     DefaultBatchSize,
		logger:        logger,
		highWaterMark: make(map[string]waterMark),
	}
}

// WithBatchSize sets the maximum number of outbox entries fetched per poll cycle.
// Returns the receiver for method chaining.
func (s *OutboxEventSource) WithBatchSize(n int) *OutboxEventSource {
	if n > 0 {
		s.batchSize = n
	}
	return s
}

// Start begins polling the event_outbox table at the configured interval,
// delivering each completed entry to handler as a DomainEvent.
//
// Start blocks until ctx is cancelled, then returns nil. Handler errors are
// logged but do not stop the polling loop.
func (s *OutboxEventSource) Start(ctx context.Context, handler eventstream.EventHandler) error {
	s.logger.Info("outbox event source starting",
		"poll_interval", s.pollInterval,
		"batch_size", s.batchSize,
	)

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("outbox event source stopped")
			return nil
		case <-ticker.C:
			if err := s.pollEvents(ctx, handler); err != nil {
				s.logger.Warn("outbox poll error", "error", err)
			}
		}
	}
}

// pollEvents queries the event_outbox table for completed entries that have not
// yet been seen (based on the per-service high-water mark) and calls handler
// for each one.
func (s *OutboxEventSource) pollEvents(ctx context.Context, handler eventstream.EventHandler) error {
	// Snapshot the current high-water marks under the lock so we can release
	// the lock before issuing the (potentially slow) database query.
	s.mu.Lock()
	hwm := make(map[string]waterMark, len(s.highWaterMark))
	for k, v := range s.highWaterMark {
		hwm[k] = v
	}
	s.mu.Unlock()

	// Fetch completed outbox entries ordered by (created_at, id) so delivery
	// is deterministic and the high-water mark advances monotonically.
	//
	// We use a single query across all services. Per-service high-water mark
	// filtering happens in application code after fetching because CockroachDB
	// cannot efficiently express "WHERE (service_name, created_at) > (?, ?)"
	// across multiple services in a single parameterised query.
	var entries []events.EventOutbox
	if err := s.db.WithContext(ctx).
		Where("status = ?", events.StatusCompleted).
		Order("created_at ASC, id ASC").
		Limit(s.batchSize).
		Find(&entries).Error; err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	// Track the new high-water marks accumulated during this batch before
	// invoking any handlers so that a handler error does not prevent the mark
	// from advancing on the next poll.
	newMarks := make(map[string]waterMark)

	for _, entry := range entries {
		mark, seen := hwm[entry.ServiceName]
		if seen {
			// Skip entries at or before the high-water mark for this service.
			if entry.CreatedAt.Before(mark.createdAt) ||
				(entry.CreatedAt.Equal(mark.createdAt) && entry.ID == mark.id) {
				continue
			}
			// Same timestamp but different ID — only skip if we've already
			// advanced past this ID. UUIDs are not monotonic so we skip any ID
			// equal to the last seen one; entries with a greater created_at always pass.
		}

		event := s.outboxToDomainEvent(entry)

		if err := handler(ctx, event); err != nil {
			s.logger.Error("event handler error",
				"error", err,
				"outbox_id", entry.ID,
				"event_type", entry.EventType,
				"service", entry.ServiceName,
			)
			// Do not advance the high-water mark for this service on error;
			// the next poll will retry from the same position.
			continue
		}

		// Advance the high-water mark for this service.
		newMarks[entry.ServiceName] = waterMark{
			createdAt: entry.CreatedAt,
			id:        entry.ID,
		}
	}

	// Commit the new high-water marks under the lock.
	if len(newMarks) > 0 {
		s.mu.Lock()
		for svc, mark := range newMarks {
			current, exists := s.highWaterMark[svc]
			if !exists || mark.createdAt.After(current.createdAt) ||
				(mark.createdAt.Equal(current.createdAt) && mark.id != current.id) {
				s.highWaterMark[svc] = mark
			}
		}
		s.mu.Unlock()
	}

	return nil
}

// outboxToDomainEvent converts an EventOutbox entry to a DomainEvent.
//
// Note: EventOutbox does not carry a TenantID field. In dev/CI mode all
// services share a single CockroachDB instance and tenant context is not
// stored directly in the outbox. TenantID is left empty; callers that require
// tenant-scoped delivery must populate it from the payload or application context.
// This is an acceptable limitation for the dev/CI adapter.
func (s *OutboxEventSource) outboxToDomainEvent(entry events.EventOutbox) eventstream.DomainEvent {
	channel, err := eventstream.DeriveChannel(entry.Topic)
	if err != nil {
		// DeriveChannel only errors on empty topic; Topic is required by the
		// outbox schema, so this path is unreachable in practice.
		s.logger.Warn("outbox entry has empty topic, using event_type as channel",
			"outbox_id", entry.ID,
			"event_type", entry.EventType,
		)
		channel = entry.EventType
	}

	return eventstream.DomainEvent{
		EventID:       entry.ID.String(),
		EventType:     entry.EventType,
		Topic:         entry.Topic,
		Channel:       channel,
		AggregateID:   entry.AggregateID,
		AggregateType: entry.AggregateType,
		TenantID:      "", // Not stored in EventOutbox; see function comment.
		CorrelationID: entry.CorrelationID,
		CausationID:   entry.CausationID,
		Timestamp:     entry.CreatedAt.UTC(),
		Payload:       entry.EventPayload,
	}
}
