package stripe

import "errors"

var (
	// ErrNilLister is returned when a nil BalanceTransactionLister is provided.
	ErrNilLister = errors.New("balance transaction lister must not be nil")
	// ErrEmptyAccountID is returned when the Connected Account ID is empty.
	ErrEmptyAccountID = errors.New("stripe connected account ID must not be empty")
	// ErrNilSnapshotRepo is returned when a nil snapshot repository is provided.
	ErrNilSnapshotRepo = errors.New("settlement snapshot repository must not be nil")
	// ErrNilTransactionClient is returned when a nil transaction client is provided.
	ErrNilTransactionClient = errors.New("balance transaction client must not be nil")
	// ErrNilTransformer is returned when a nil transformer is provided.
	ErrNilTransformer = errors.New("settlement transformer must not be nil")
	// ErrNilRunRepo is returned when a nil settlement run repository is provided.
	ErrNilRunRepo = errors.New("settlement run repository must not be nil")
	// ErrEmptyInternalAccountID is returned when the internal account ID is empty.
	ErrEmptyInternalAccountID = errors.New("internal account ID must not be empty")
)
