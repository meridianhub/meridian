package domain

import (
	"time"

	"github.com/google/uuid"
)

// TransactionLineage captures the relationship between transactions.
// It maintains the parent-child hierarchy and related transaction references.
// This is an immutable value object - all fields are unexported and accessed through getter methods.
type TransactionLineage struct {
	transactionID         uuid.UUID
	parentTransactionID   *uuid.UUID
	childTransactionIDs   []uuid.UUID
	relatedTransactionIDs []uuid.UUID
	transactionType       string
	createdAt             time.Time
}

// NewTransactionLineage creates an immutable TransactionLineage with all relationships established upfront.
// It returns ErrInvalidTransactionID if transactionID is uuid.Nil.
// All slice and pointer parameters are defensively copied to prevent external mutation.
// Pass nil for parentTransactionID and empty slices for childTransactionIDs/relatedTransactionIDs if not needed.
func NewTransactionLineage(
	transactionID uuid.UUID,
	transactionType string,
	parentTransactionID *uuid.UUID,
	childTransactionIDs []uuid.UUID,
	relatedTransactionIDs []uuid.UUID,
) (*TransactionLineage, error) {
	// Validate transaction ID is not nil
	if transactionID == uuid.Nil {
		return nil, ErrInvalidTransactionID
	}

	// Defensive copy of parent ID pointer
	var parent *uuid.UUID
	if parentTransactionID != nil {
		p := *parentTransactionID
		parent = &p
	}

	// Defensive copy of child IDs slice
	children := make([]uuid.UUID, len(childTransactionIDs))
	copy(children, childTransactionIDs)

	// Defensive copy of related IDs slice
	related := make([]uuid.UUID, len(relatedTransactionIDs))
	copy(related, relatedTransactionIDs)

	return &TransactionLineage{
		transactionID:         transactionID,
		parentTransactionID:   parent,
		childTransactionIDs:   children,
		relatedTransactionIDs: related,
		transactionType:       transactionType,
		createdAt:             time.Now().UTC(),
	}, nil
}

// HasParent returns true if this transaction has a parent.
func (l *TransactionLineage) HasParent() bool {
	return l.parentTransactionID != nil
}

// HasChildren returns true if this transaction has children.
func (l *TransactionLineage) HasChildren() bool {
	return len(l.childTransactionIDs) > 0
}

// TransactionID returns the transaction ID.
func (l *TransactionLineage) TransactionID() uuid.UUID {
	return l.transactionID
}

// ParentTransactionID returns a copy of the parent transaction ID pointer.
// Returns nil if there is no parent.
func (l *TransactionLineage) ParentTransactionID() *uuid.UUID {
	if l.parentTransactionID == nil {
		return nil
	}
	// Defensive copy of pointer
	p := *l.parentTransactionID
	return &p
}

// ChildTransactionIDs returns a defensive copy of the child transaction IDs slice.
// Mutating the returned slice will not affect the internal state.
func (l *TransactionLineage) ChildTransactionIDs() []uuid.UUID {
	if len(l.childTransactionIDs) == 0 {
		return nil
	}
	// Defensive copy of slice
	result := make([]uuid.UUID, len(l.childTransactionIDs))
	copy(result, l.childTransactionIDs)
	return result
}

// RelatedTransactionIDs returns a defensive copy of the related transaction IDs slice.
// Mutating the returned slice will not affect the internal state.
func (l *TransactionLineage) RelatedTransactionIDs() []uuid.UUID {
	if len(l.relatedTransactionIDs) == 0 {
		return nil
	}
	// Defensive copy of slice
	result := make([]uuid.UUID, len(l.relatedTransactionIDs))
	copy(result, l.relatedTransactionIDs)
	return result
}

// TransactionType returns the transaction type.
func (l *TransactionLineage) TransactionType() string {
	return l.transactionType
}

// CreatedAt returns the creation timestamp.
func (l *TransactionLineage) CreatedAt() time.Time {
	return l.createdAt
}
