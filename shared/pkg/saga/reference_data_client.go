package saga

import (
	"context"
	"time"
)

// ReferenceDataClient provides saga runtime access to reference data lookups.
// This interface abstracts the reference-data service gRPC client, allowing
// builtins to remain decoupled from implementation details.
//
// The knowledgeAt parameter enables bi-temporal queries - lookups are resolved
// as they existed at a specific point in time, supporting FR-34 deterministic
// replay requirements. When a saga is replayed, the same lookup results are
// guaranteed by using the saga's original execution timestamp.
type ReferenceDataClient interface {
	// ResolveAccount resolves an account reference to an account ID.
	// The reference can be any identifier (account number, alias, etc).
	// Uses bi-temporal KnowledgeAt for deterministic replay.
	//
	// Returns the account ID as a string, or an error if resolution fails.
	ResolveAccount(ctx context.Context, reference string, knowledgeAt time.Time) (string, error)

	// ResolveInstrument resolves an instrument reference to an instrument ID.
	// The reference can be an instrument code, ISIN, ticker, or any other identifier.
	// Uses bi-temporal KnowledgeAt for deterministic replay.
	//
	// Returns the instrument ID as a string, or an error if resolution fails.
	ResolveInstrument(ctx context.Context, reference string, knowledgeAt time.Time) (string, error)
}
