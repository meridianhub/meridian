// Package events provides the transactional outbox pattern for reliable event delivery.
//
// The outbox pattern ensures at-least-once delivery of events by storing them in a database table
// within the same transaction as the business operation. A background worker then processes these
// events and publishes them to Kafka asynchronously.
//
// This is particularly important for audit-critical control operations (SUSPEND, RESUME, TERMINATE)
// where event loss would result in incomplete audit trails.
//
// # Architecture
//
// The package consists of three main components:
//
// 1. EventOutbox: The domain model representing an event waiting to be published.
// 2. OutboxPublisher: Used by services to write events to the outbox within transactions.
// 3. Worker: Background processor that polls the outbox and publishes events to Kafka.
//
// # Usage Example
//
// In your service layer, use OutboxPublisher within the same transaction as your business operation:
//
//	publisher := events.NewOutboxPublisher("position-keeping")
//
//	err := db.Transaction(func(tx *gorm.DB) error {
//	    // Business operation: suspend the position log
//	    if err := tx.Model(&log).Update("status", "SUSPENDED").Error; err != nil {
//	        return err
//	    }
//
//	    // Publish event (within same transaction)
//	    event := &eventsv1.TransactionSuspendedEvent{...}
//	    return publisher.PublishControlEvent(ctx, tx, event,
//	        "position_keeping.transaction_suspended.v1",
//	        log.ID.String(),
//	        "FinancialPositionLog",
//	        "position-keeping.control-events.v1",
//	        correlationID,
//	    )
//	})
//
// In your application bootstrap, start the background worker:
//
//	repo := events.NewPostgresOutboxRepository(db)
//	producer, _ := kafka.NewProtoProducer(kafkaConfig)
//
//	config := events.DefaultWorkerConfig("position-keeping")
//	worker := events.NewWorker(repo, producer.producer, config, logger)
//
//	worker.Start(ctx)
//	defer worker.Stop()
//
// # Database Schema
//
// The event_outbox table must be created in each service's database.
// See schema.sql in this package for the PostgreSQL DDL.
//
// # Observability
//
// The package exposes Prometheus metrics for monitoring:
//   - meridian_event_outbox_depth: Number of pending entries
//   - meridian_event_outbox_published_total: Events published (by status: success/failure)
//   - meridian_event_outbox_dlq_total: Events moved to DLQ after retries exhausted
//   - meridian_event_outbox_processing_duration_seconds: Batch processing latency
//   - meridian_event_outbox_entry_age_seconds: Time from event creation to processing
//
// # Retry and DLQ Behavior
//
// Failed publishes are retried with the configured MaxRetries (default: 5).
// After retries are exhausted, entries are marked as 'failed' (effectively DLQ'd in the database).
// The last error is recorded for debugging.
//
// # Thread Safety
//
// All components are safe for concurrent use. The Worker can process entries from multiple
// goroutines if needed, though the default single-goroutine mode maintains ordering.
package events
