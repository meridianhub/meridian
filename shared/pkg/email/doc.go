// Package email provides the core types and interfaces for transactional email
// delivery in Meridian. It implements an outbox pattern for reliable, at-least-once
// email delivery with audit logging.
//
// # Key Types
//
//   - [Sender]: interface for email delivery providers (e.g., SES, SendGrid)
//   - [OutboxRepository]: persistence for the outbox pattern with atomic claim-and-dispatch
//   - [AuditRepository]: immutable audit trail for all email delivery attempts
//   - [OutboxEntry]: queued email with retry tracking and backoff schedule
//   - [AuditEntry]: record of a delivery attempt with provider response details
package email
