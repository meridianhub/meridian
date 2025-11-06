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

// NewTransactionLineage creates a new transaction lineage record.
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
func (l *TransactionLineage) SetParent(parentID uuid.UUID) {
	l.ParentTransactionID = &parentID
}

// AddChild adds a child transaction ID.
func (l *TransactionLineage) AddChild(childID uuid.UUID) {
	// Avoid duplicates
	for _, id := range l.ChildTransactionIDs {
		if id == childID {
			return
		}
	}
	l.ChildTransactionIDs = append(l.ChildTransactionIDs, childID)
}

// AddRelated adds a related transaction ID.
func (l *TransactionLineage) AddRelated(relatedID uuid.UUID) {
	// Avoid duplicates
	for _, id := range l.RelatedTransactionIDs {
		if id == relatedID {
			return
		}
	}
	l.RelatedTransactionIDs = append(l.RelatedTransactionIDs, relatedID)
}

// HasParent returns true if this transaction has a parent.
func (l *TransactionLineage) HasParent() bool {
	return l.ParentTransactionID != nil
}

// HasChildren returns true if this transaction has children.
func (l *TransactionLineage) HasChildren() bool {
	return len(l.ChildTransactionIDs) > 0
}
