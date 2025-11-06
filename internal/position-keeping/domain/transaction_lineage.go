package domain

import (
	"time"

	"github.com/google/uuid"
)

// TransactionLineage captures the relationship between transactions.
// It maintains the parent-child hierarchy and related transaction references.
type TransactionLineage struct {
	TransactionID         uuid.UUID
	ParentTransactionID   *uuid.UUID
	ChildTransactionIDs   []uuid.UUID
	RelatedTransactionIDs []uuid.UUID
	TransactionType       string
	CreatedAt             time.Time
}

// NewTransactionLineage creates a TransactionLineage for the provided transaction ID and type.
// It returns ErrInvalidTransactionID if transactionID is uuid.Nil.
// The returned lineage has ParentTransactionID set to nil, empty ChildTransactionIDs and
// RelatedTransactionIDs slices, TransactionType set to the provided type, and CreatedAt set to
// the current UTC time.
func NewTransactionLineage(
	transactionID uuid.UUID,
	transactionType string,
) (*TransactionLineage, error) {
	// Validate transaction ID is not nil
	if transactionID == uuid.Nil {
		return nil, ErrInvalidTransactionID
	}

	return &TransactionLineage{
		TransactionID:         transactionID,
		ParentTransactionID:   nil,
		ChildTransactionIDs:   make([]uuid.UUID, 0),
		RelatedTransactionIDs: make([]uuid.UUID, 0),
		TransactionType:       transactionType,
		CreatedAt:             time.Now().UTC(),
	}, nil
}

// SetParent sets the parent transaction ID.
// Returns an error if parentID is nil.
func (l *TransactionLineage) SetParent(parentID uuid.UUID) error {
	if parentID == uuid.Nil {
		return ErrInvalidTransactionID
	}
	l.ParentTransactionID = &parentID
	return nil
}

// AddChild adds a child transaction ID.
// Returns an error if childID is nil or invalid.
func (l *TransactionLineage) AddChild(childID uuid.UUID) error {
	if childID == uuid.Nil {
		return ErrInvalidTransactionID
	}

	// Avoid duplicates
	for _, id := range l.ChildTransactionIDs {
		if id == childID {
			return nil
		}
	}
	l.ChildTransactionIDs = append(l.ChildTransactionIDs, childID)
	return nil
}

// AddRelated adds a related transaction ID.
// Returns an error if relatedID is nil or invalid.
func (l *TransactionLineage) AddRelated(relatedID uuid.UUID) error {
	if relatedID == uuid.Nil {
		return ErrInvalidTransactionID
	}

	// Avoid duplicates
	for _, id := range l.RelatedTransactionIDs {
		if id == relatedID {
			return nil
		}
	}
	l.RelatedTransactionIDs = append(l.RelatedTransactionIDs, relatedID)
	return nil
}

// HasParent returns true if this transaction has a parent.
func (l *TransactionLineage) HasParent() bool {
	return l.ParentTransactionID != nil
}

// HasChildren returns true if this transaction has children.
func (l *TransactionLineage) HasChildren() bool {
	return len(l.ChildTransactionIDs) > 0
}
