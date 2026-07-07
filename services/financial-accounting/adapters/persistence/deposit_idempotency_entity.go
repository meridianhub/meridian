package persistence

import "time"

// DepositIdempotencyEntity is a per-deposit idempotency marker written in the
// SAME database transaction as the ledger postings for a deposit.
//
// # Why this exists
//
// Deposit events arrive over Kafka with at-least-once delivery. The consumer
// previously relied on a Redis marker written AFTER the ledger postings were
// committed. A crash (or Redis outage) between the ledger commit and the Redis
// write left the deposit committed with no idempotency marker, so a redelivery
// re-processed it and produced a duplicate double-entry posting.
//
// Writing this marker inside the same transaction as the postings makes
// exactly-once processing a database invariant: the unique primary key on
// DedupeKey guarantees that a second attempt to record the same deposit fails
// the whole transaction (marker + postings roll back together). Redis is then
// only an optimisation (a fast-path "already done" cache), never a correctness
// dependency.
//
// DedupeKey is a deterministic per-deposit fingerprint (see the deposit
// consumer's dedupe-key derivation), NOT the correlation ID: correlation IDs
// can be reused across distinct deposits, so keying on them would incorrectly
// collapse distinct deposits into one.
type DepositIdempotencyEntity struct {
	// DedupeKey is the per-deposit idempotency fingerprint (hex-encoded SHA-256).
	DedupeKey string `gorm:"column:dedupe_key;primaryKey;size:64"`

	// AccountID is the account that received the deposit (for observability/forensics).
	AccountID string `gorm:"column:account_id;not null;size:255"`

	// CorrelationID links the marker back to the originating event stream.
	CorrelationID string `gorm:"column:correlation_id;size:255"`

	// CreatedAt is when the deposit was recorded.
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

// TableName overrides the default table name.
// Uses singular, unqualified name per database-per-service architecture
// (search_path routing to the tenant schema).
func (DepositIdempotencyEntity) TableName() string {
	return "deposit_idempotency"
}
