// Package audit provides application-level audit logging for Meridian services.
//
// # Architecture Overview
//
// The audit package implements the Transactional Outbox Pattern with Kafka integration,
// providing guaranteed delivery of audit events. This design ensures no audit records
// are lost, even during service failures or Kafka outages.
//
// See ADR-0009 (Application-Level Audit Logging) for the full rationale and design decisions.
//
// # Dual-Path Architecture
//
// The system uses a dual-path approach for reliability:
//
//  1. Primary Path (Kafka): Audit events are published to Kafka topics for near-real-time
//     processing by dedicated audit consumers.
//
//  2. Fallback Path (Outbox): When Kafka is unavailable, events are written atomically
//     to the audit_outbox table within the same database transaction as the business
//     operation. A background worker later processes these entries.
//
// This ensures audit records are never lost:
//
//	Business Operation --> GORM Hook --> Try Kafka Publish
//	                                          |
//	                         Success?         |
//	                        /      \         |
//	                       Yes      No        |
//	                       |        |         v
//	              Kafka Consumer   Write to audit_outbox
//	                       |              |
//	                       v              v
//	                  audit_log     Worker --> audit_log
//
// # Usage
//
// To add audit logging to an entity, implement the Auditable interface and add GORM hooks:
//
//	type MyEntity struct {
//	    ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
//	    Name      string
//	    UpdatedAt time.Time
//	}
//
//	// Implement Auditable interface
//	func (e MyEntity) AuditID() string       { return e.ID.String() }
//	func (e MyEntity) AuditTableName() string { return "my_entity" }
//
//	// Add GORM hooks
//	func (e *MyEntity) AfterCreate(tx *gorm.DB) error  { return audit.RecordCreate(tx, *e) }
//	func (e *MyEntity) BeforeUpdate(tx *gorm.DB) error { return audit.CaptureOldValue(tx, *e) }
//	func (e *MyEntity) AfterUpdate(tx *gorm.DB) error  { return audit.RecordUpdate(tx, *e) }
//	func (e *MyEntity) AfterDelete(tx *gorm.DB) error  { return audit.RecordDelete(tx, *e) }
//
// # Service Initialization
//
// Services must initialize the audit system during startup:
//
//	// Set the schema name for this service
//	audit.SetSchemaName("my_service")
//
//	// Optionally configure Kafka publisher
//	publisher, err := audit.NewPublisher(audit.PublisherConfig{
//	    BootstrapServers: os.Getenv("KAFKA_BOOTSTRAP_SERVERS"),
//	    Topic:            "audit.events.my-service.v1",
//	    SchemaName:       "my_service",
//	    ClientID:         "my-service-audit-publisher",
//	})
//	if err == nil {
//	    audit.SetGlobalPublisher(publisher)
//	}
//
// # Performance Characteristics
//
// The audit system is designed for minimal impact on business operations:
//
//   - Kafka publish timeout: 5 seconds (falls back to outbox on failure)
//   - Outbox write: Single INSERT within existing transaction (negligible overhead)
//   - Worker batch size: 100 entries (configurable via WithBatchSize)
//   - Worker poll interval: 5 seconds (configurable via WithPollInterval)
//   - Adaptive polling: Reduces database load during idle periods (WithAdaptivePolling)
//
// # Metrics
//
// Prometheus metrics are automatically collected:
//
//   - meridian_audit_worker_outbox_depth: Pending entries in outbox (gauge by schema)
//   - meridian_audit_worker_outbox_processed_total: Successfully processed entries (counter)
//   - meridian_audit_worker_outbox_failed_total: Failed entries after max retries (counter)
//   - meridian_audit_kafka_events_published_total: Events published to Kafka (counter)
//   - meridian_audit_kafka_fallback_used_total: Times fallback to outbox was used (counter)
//
// # Key Types
//
// The package exports several key types:
//
//   - [Auditable]: Interface that entities must implement for audit logging
//   - [AuditOutbox]: Temporary storage for audit events pending processing
//   - [AuditLog]: Permanent immutable audit trail
//   - [Worker]: Background processor for outbox entries
//   - [Publisher]: Kafka publisher for audit events
//   - [Consumer]: Kafka consumer that writes to audit_log
//
// # Error Handling
//
// Sentinel errors are provided for error checking:
//
//   - [ErrNilTransaction]: Passed nil transaction to audit function
//   - [ErrOldValueType]: Type mismatch when retrieving old values from context
//   - [ErrMaxRetriesExceeded]: Entry failed after maximum retry attempts
//   - [ErrBatchProcessingFailed]: Some entries in batch failed to process
//
// # Thread Safety
//
// All exported functions are safe for concurrent use. Global state (schema name,
// publisher) is protected by mutex locks.
package audit
