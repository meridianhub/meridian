package service

import (
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// validateImmutableFieldsUnchanged checks that no immutable fields have been modified
// between an existing position and a proposed update. This enforces append-only semantics
// at the application layer since CockroachDB does not support PL/pgSQL triggers.
//
// Immutable fields: amount, account_id, instrument_code, bucket_key, reference_id,
// dimension, created_at, created_by.
//
// Mutable fields (allowed to change): deleted_at, attributes.
func validateImmutableFieldsUnchanged(existing, proposed *domain.Position) error {
	if !existing.Amount.Equal(proposed.Amount) {
		return domain.ErrPositionUpdateForbidden
	}
	if existing.AccountID != proposed.AccountID {
		return domain.ErrPositionUpdateForbidden
	}
	if existing.InstrumentCode != proposed.InstrumentCode {
		return domain.ErrPositionUpdateForbidden
	}
	if existing.BucketKey != proposed.BucketKey {
		return domain.ErrPositionUpdateForbidden
	}
	if existing.ReferenceID != proposed.ReferenceID {
		return domain.ErrPositionUpdateForbidden
	}
	if existing.Dimension != proposed.Dimension {
		return domain.ErrPositionUpdateForbidden
	}
	if !existing.CreatedAt.Equal(proposed.CreatedAt) {
		return domain.ErrPositionUpdateForbidden
	}
	if existing.CreatedBy != proposed.CreatedBy {
		return domain.ErrPositionUpdateForbidden
	}
	return nil
}
