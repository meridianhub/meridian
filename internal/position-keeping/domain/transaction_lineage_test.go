package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewTransactionLineage_ValidInputs(t *testing.T) {
	lineage, err := NewTransactionLineage(uuid.New(), "PAYMENT")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if lineage.TransactionType != "PAYMENT" {
		t.Errorf("Expected transaction type PAYMENT, got %v", lineage.TransactionType)
	}

	if lineage.ParentTransactionID != nil {
		t.Error("Expected ParentTransactionID to be nil")
	}

	if len(lineage.ChildTransactionIDs) != 0 {
		t.Errorf("Expected empty ChildTransactionIDs, got %d entries", len(lineage.ChildTransactionIDs))
	}

	if len(lineage.RelatedTransactionIDs) != 0 {
		t.Errorf("Expected empty RelatedTransactionIDs, got %d entries", len(lineage.RelatedTransactionIDs))
	}
}

func TestNewTransactionLineage_NilTransactionID(t *testing.T) {
	_, err := NewTransactionLineage(uuid.Nil, "PAYMENT")
	if !errors.Is(err, ErrInvalidTransactionID) {
		t.Errorf("Expected ErrInvalidTransactionID, got: %v", err)
	}
}

func TestNewTransactionLineage_EmptyTransactionType(t *testing.T) {
	lineage, err := NewTransactionLineage(uuid.New(), "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if lineage.TransactionType != "" {
		t.Errorf("Expected empty transaction type, got %v", lineage.TransactionType)
	}
}

func TestNewTransactionLineage_StringEdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		transactionType string
		wantErr         bool
	}{
		{
			name:            "very long TransactionType (1000 chars)",
			transactionType: strings.Repeat("T", 1000),
			wantErr:         false,
		},
		{
			name:            "very long TransactionType (10000 chars)",
			transactionType: strings.Repeat("T", 10000),
			wantErr:         false,
		},
		{
			name:            "whitespace-only TransactionType is allowed",
			transactionType: "   ",
			wantErr:         false,
		},
		{
			name:            "tab-only TransactionType is allowed",
			transactionType: "\t\t\t",
			wantErr:         false,
		},
		{
			name:            "newline-only TransactionType is allowed",
			transactionType: "\n\n",
			wantErr:         false,
		},
		{
			name:            "TransactionType with leading/trailing spaces",
			transactionType: "  PAYMENT  ",
			wantErr:         false,
		},
		{
			name:            "unicode characters in TransactionType",
			transactionType: "支付_PAYMENT",
			wantErr:         false,
		},
		{
			name:            "special characters in TransactionType",
			transactionType: "PAYMENT!@#$%^&*()",
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lineage, err := NewTransactionLineage(uuid.New(), tt.transactionType)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if lineage.TransactionType != tt.transactionType {
				t.Errorf("Expected TransactionType to be preserved as %q, got %q", tt.transactionType, lineage.TransactionType)
			}
		})
	}
}

func TestTransactionLineage_SetParent(t *testing.T) {
	lineage, _ := NewTransactionLineage(uuid.New(), "PAYMENT")

	tests := []struct {
		name        string
		parentID    uuid.UUID
		wantErr     bool
		expectedErr error
	}{
		{
			name:     "valid parent ID",
			parentID: uuid.New(),
			wantErr:  false,
		},
		{
			name:        "nil parent ID",
			parentID:    uuid.Nil,
			wantErr:     true,
			expectedErr: ErrInvalidTransactionID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := lineage.SetParent(tt.parentID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if lineage.ParentTransactionID == nil {
				t.Error("Expected ParentTransactionID to be set")
			} else if *lineage.ParentTransactionID != tt.parentID {
				t.Errorf("Expected parent ID %v, got %v", tt.parentID, *lineage.ParentTransactionID)
			}
		})
	}
}

func TestTransactionLineage_AddChild(t *testing.T) {
	lineage, _ := NewTransactionLineage(uuid.New(), "PAYMENT")

	tests := []struct {
		name        string
		childID     uuid.UUID
		wantErr     bool
		expectedErr error
	}{
		{
			name:    "valid child ID",
			childID: uuid.New(),
			wantErr: false,
		},
		{
			name:        "nil child ID",
			childID:     uuid.Nil,
			wantErr:     true,
			expectedErr: ErrInvalidTransactionID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initialCount := len(lineage.ChildTransactionIDs)
			err := lineage.AddChild(tt.childID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if len(lineage.ChildTransactionIDs) != initialCount+1 {
				t.Errorf("Expected %d children, got %d", initialCount+1, len(lineage.ChildTransactionIDs))
			}
		})
	}
}

func TestTransactionLineage_AddChild_Duplicates(t *testing.T) {
	lineage, _ := NewTransactionLineage(uuid.New(), "PAYMENT")
	childID := uuid.New()

	// Add the same child twice
	err1 := lineage.AddChild(childID)
	if err1 != nil {
		t.Fatalf("First AddChild failed: %v", err1)
	}

	err2 := lineage.AddChild(childID)
	if err2 != nil {
		t.Fatalf("Second AddChild failed: %v", err2)
	}

	// Should only have one entry
	if len(lineage.ChildTransactionIDs) != 1 {
		t.Errorf("Expected 1 child, got %d", len(lineage.ChildTransactionIDs))
	}
}

func TestTransactionLineage_AddRelated(t *testing.T) {
	lineage, _ := NewTransactionLineage(uuid.New(), "PAYMENT")

	tests := []struct {
		name        string
		relatedID   uuid.UUID
		wantErr     bool
		expectedErr error
	}{
		{
			name:      "valid related ID",
			relatedID: uuid.New(),
			wantErr:   false,
		},
		{
			name:        "nil related ID",
			relatedID:   uuid.Nil,
			wantErr:     true,
			expectedErr: ErrInvalidTransactionID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initialCount := len(lineage.RelatedTransactionIDs)
			err := lineage.AddRelated(tt.relatedID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if len(lineage.RelatedTransactionIDs) != initialCount+1 {
				t.Errorf("Expected %d related transactions, got %d", initialCount+1, len(lineage.RelatedTransactionIDs))
			}
		})
	}
}

func TestTransactionLineage_AddRelated_Duplicates(t *testing.T) {
	lineage, _ := NewTransactionLineage(uuid.New(), "PAYMENT")
	relatedID := uuid.New()

	// Add the same related transaction twice
	err1 := lineage.AddRelated(relatedID)
	if err1 != nil {
		t.Fatalf("First AddRelated failed: %v", err1)
	}

	err2 := lineage.AddRelated(relatedID)
	if err2 != nil {
		t.Fatalf("Second AddRelated failed: %v", err2)
	}

	// Should only have one entry
	if len(lineage.RelatedTransactionIDs) != 1 {
		t.Errorf("Expected 1 related transaction, got %d", len(lineage.RelatedTransactionIDs))
	}
}

func TestTransactionLineage_HasParent(t *testing.T) {
	lineage, _ := NewTransactionLineage(uuid.New(), "PAYMENT")

	if lineage.HasParent() {
		t.Error("Expected HasParent to be false initially")
	}

	err := lineage.SetParent(uuid.New())
	if err != nil {
		t.Fatalf("SetParent failed: %v", err)
	}

	if !lineage.HasParent() {
		t.Error("Expected HasParent to be true after setting parent")
	}
}

func TestTransactionLineage_HasChildren(t *testing.T) {
	lineage, _ := NewTransactionLineage(uuid.New(), "PAYMENT")

	if lineage.HasChildren() {
		t.Error("Expected HasChildren to be false initially")
	}

	err := lineage.AddChild(uuid.New())
	if err != nil {
		t.Fatalf("AddChild failed: %v", err)
	}

	if !lineage.HasChildren() {
		t.Error("Expected HasChildren to be true after adding child")
	}
}

func TestTransactionLineage_CreatedAtIsUTC(t *testing.T) {
	before := time.Now().UTC()
	lineage, err := NewTransactionLineage(uuid.New(), "PAYMENT")
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("NewTransactionLineage failed: %v", err)
	}

	if lineage.CreatedAt.Location() != time.UTC {
		t.Errorf("Expected CreatedAt to be UTC, got: %v", lineage.CreatedAt.Location())
	}

	if lineage.CreatedAt.Before(before) || lineage.CreatedAt.After(after) {
		t.Errorf("Expected CreatedAt to be between %v and %v, got: %v", before, after, lineage.CreatedAt)
	}
}
