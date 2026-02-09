package differ

import "context"

// SafetyChecker verifies whether a resource can be safely deleted.
// Implementations query downstream services (Position Keeping, Reference Data)
// to detect live usage that would block deletion.
type SafetyChecker interface {
	// CheckAccountTypeDeletion returns a BlockedDeletion if the account type
	// has positions with non-zero balances. Returns nil, nil if safe to delete.
	CheckAccountTypeDeletion(ctx context.Context, accountTypeCode string) (*BlockedDeletion, error)

	// CheckInstrumentDeletion returns a BlockedDeletion if the instrument is
	// referenced by active valuation rules or has live positions. Returns nil, nil if safe.
	CheckInstrumentDeletion(ctx context.Context, instrumentCode string) (*BlockedDeletion, error)

	// CheckSagaDeletion returns a BlockedDeletion if the saga has pending or
	// running instances. Returns nil, nil if safe to delete.
	CheckSagaDeletion(ctx context.Context, sagaName string) (*BlockedDeletion, error)
}

// NoOpSafetyChecker always allows deletions. Useful for dry-run or testing
// where downstream services are not available.
type NoOpSafetyChecker struct{}

// CheckAccountTypeDeletion always returns nil (safe to delete).
//
//nolint:nilnil // nil,nil is the documented interface contract for "safe to delete"
func (n *NoOpSafetyChecker) CheckAccountTypeDeletion(_ context.Context, _ string) (*BlockedDeletion, error) {
	return nil, nil
}

// CheckInstrumentDeletion always returns nil (safe to delete).
//
//nolint:nilnil // nil,nil is the documented interface contract for "safe to delete"
func (n *NoOpSafetyChecker) CheckInstrumentDeletion(_ context.Context, _ string) (*BlockedDeletion, error) {
	return nil, nil
}

// CheckSagaDeletion always returns nil (safe to delete).
//
//nolint:nilnil // nil,nil is the documented interface contract for "safe to delete"
func (n *NoOpSafetyChecker) CheckSagaDeletion(_ context.Context, _ string) (*BlockedDeletion, error) {
	return nil, nil
}
