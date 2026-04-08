package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewTransactionLineage(t *testing.T) {
	t.Run("valid lineage with no relationships", func(t *testing.T) {
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, nil)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if lineage == nil {
			t.Error("Expected lineage, got nil")
		}
	})

	t.Run("valid lineage with parent only", func(t *testing.T) {
		parentID := uuid.New()
		lineage, err := NewTransactionLineage(uuid.New(), "payment", &parentID, nil, nil)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !lineage.HasParent() {
			t.Error("Expected lineage to have parent")
		}
	})

	t.Run("valid lineage with children", func(t *testing.T) {
		children := []uuid.UUID{uuid.New(), uuid.New()}
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, children, nil)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !lineage.HasChildren() {
			t.Error("Expected lineage to have children")
		}
		if len(lineage.ChildTransactionIDs()) != 2 {
			t.Errorf("Expected 2 children, got %d", len(lineage.ChildTransactionIDs()))
		}
	})

	t.Run("valid lineage with related transactions", func(t *testing.T) {
		related := []uuid.UUID{uuid.New()}
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, related)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if len(lineage.RelatedTransactionIDs()) != 1 {
			t.Errorf("Expected 1 related, got %d", len(lineage.RelatedTransactionIDs()))
		}
	})

	t.Run("valid lineage with all relationships", func(t *testing.T) {
		parentID := uuid.New()
		children := []uuid.UUID{uuid.New(), uuid.New()}
		related := []uuid.UUID{uuid.New(), uuid.New()}
		lineage, err := NewTransactionLineage(uuid.New(), "payment", &parentID, children, related)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !lineage.HasParent() || !lineage.HasChildren() {
			t.Error("Expected lineage to have parent and children")
		}
	})

	t.Run("invalid nil transaction ID", func(t *testing.T) {
		_, err := NewTransactionLineage(uuid.Nil, "payment", nil, nil, nil)
		if err == nil {
			t.Error("Expected error for nil transaction ID")
		}
	})

	t.Run("verifies CreatedAt timestamp", func(t *testing.T) {
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}
		if lineage.CreatedAt().IsZero() {
			t.Error("CreatedAt should not be zero")
		}
	})
}

func TestTransactionLineage_DefensiveCopy_Parent(t *testing.T) {
	parentID := uuid.New()
	lineage, err := NewTransactionLineage(uuid.New(), "payment", &parentID, nil, nil)
	if err != nil {
		t.Fatalf("NewTransactionLineage() returned error: %v", err)
	}

	// Get parent pointer
	gotParent := lineage.ParentTransactionID()

	// Mutate the returned pointer
	*gotParent = uuid.New()

	// Verify internal state unchanged
	internalParent := lineage.ParentTransactionID()
	if *internalParent != parentID {
		t.Errorf("Internal parent was mutated! Expected %v, got %v", parentID, *internalParent)
	}
}

func TestTransactionLineage_DefensiveCopy_Children(t *testing.T) {
	child1 := uuid.New()
	child2 := uuid.New()
	children := []uuid.UUID{child1, child2}

	lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, children, nil)
	if err != nil {
		t.Fatalf("NewTransactionLineage() returned error: %v", err)
	}

	// Get children slice
	gotChildren := lineage.ChildTransactionIDs()

	// Mutate the returned slice
	gotChildren[0] = uuid.New()
	_ = append(gotChildren, uuid.New()) // Intentionally discarded

	// Verify internal state unchanged
	internalChildren := lineage.ChildTransactionIDs()
	if len(internalChildren) != 2 {
		t.Errorf("Internal children slice was mutated! Expected length 2, got %d", len(internalChildren))
	}
	if internalChildren[0] != child1 {
		t.Errorf("Internal child[0] was mutated! Expected %v, got %v", child1, internalChildren[0])
	}
	if internalChildren[1] != child2 {
		t.Errorf("Internal child[1] was mutated! Expected %v, got %v", child2, internalChildren[1])
	}
}

func TestTransactionLineage_DefensiveCopy_Related(t *testing.T) {
	related1 := uuid.New()
	related2 := uuid.New()
	related := []uuid.UUID{related1, related2}

	lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, related)
	if err != nil {
		t.Fatalf("NewTransactionLineage() returned error: %v", err)
	}

	// Get related slice
	gotRelated := lineage.RelatedTransactionIDs()

	// Mutate the returned slice
	gotRelated[0] = uuid.New()
	_ = append(gotRelated, uuid.New()) // Intentionally discarded

	// Verify internal state unchanged
	internalRelated := lineage.RelatedTransactionIDs()
	if len(internalRelated) != 2 {
		t.Errorf("Internal related slice was mutated! Expected length 2, got %d", len(internalRelated))
	}
	if internalRelated[0] != related1 {
		t.Errorf("Internal related[0] was mutated! Expected %v, got %v", related1, internalRelated[0])
	}
	if internalRelated[1] != related2 {
		t.Errorf("Internal related[1] was mutated! Expected %v, got %v", related2, internalRelated[1])
	}
}

func TestTransactionLineage_Constructor_DefensiveCopy(t *testing.T) {
	// Test that mutating input slices after construction doesn't affect lineage
	parentID := uuid.New()
	children := []uuid.UUID{uuid.New(), uuid.New()} //nolint:prealloc // intentional: testing defensive copy, not building a collection
	related := []uuid.UUID{uuid.New()}

	lineage, err := NewTransactionLineage(uuid.New(), "payment", &parentID, children, related)
	if err != nil {
		t.Fatalf("NewTransactionLineage() returned error: %v", err)
	}

	// Mutate input slices
	children[0] = uuid.New()
	_ = append(children, uuid.New()) // Intentionally discarded
	related[0] = uuid.New()

	// Verify lineage unchanged
	internalChildren := lineage.ChildTransactionIDs()
	if len(internalChildren) != 2 {
		t.Errorf("Lineage was affected by external mutation! Expected 2 children, got %d", len(internalChildren))
	}

	internalRelated := lineage.RelatedTransactionIDs()
	if len(internalRelated) != 1 {
		t.Errorf("Lineage was affected by external mutation! Expected 1 related, got %d", len(internalRelated))
	}
}

func TestTransactionLineage_HasParent(t *testing.T) {
	tests := []struct {
		name                string
		parentTransactionID *uuid.UUID
		want                bool
	}{
		{
			name:                "no parent",
			parentTransactionID: nil,
			want:                false,
		},
		{
			name:                "has parent",
			parentTransactionID: ptr(uuid.New()),
			want:                true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lineage, _ := NewTransactionLineage(uuid.New(), "payment", tt.parentTransactionID, nil, nil)
			if got := lineage.HasParent(); got != tt.want {
				t.Errorf("HasParent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransactionLineage_HasChildren(t *testing.T) {
	tests := []struct {
		name                string
		childTransactionIDs []uuid.UUID
		want                bool
	}{
		{
			name:                "no children",
			childTransactionIDs: nil,
			want:                false,
		},
		{
			name:                "empty children slice",
			childTransactionIDs: []uuid.UUID{},
			want:                false,
		},
		{
			name:                "has children",
			childTransactionIDs: []uuid.UUID{uuid.New()},
			want:                true,
		},
		{
			name:                "has multiple children",
			childTransactionIDs: []uuid.UUID{uuid.New(), uuid.New()},
			want:                true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lineage, _ := NewTransactionLineage(uuid.New(), "payment", nil, tt.childTransactionIDs, nil)
			if got := lineage.HasChildren(); got != tt.want {
				t.Errorf("HasChildren() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ptr is a helper function to create a pointer to a UUID
func ptr(id uuid.UUID) *uuid.UUID {
	return &id
}

func TestNewTransactionLineage_StringEdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		transactionType string
		wantErr         bool
	}{
		{"very long transaction type (1000 chars)", string(make([]byte, 1000)), false},
		{"very long transaction type (10000 chars)", string(make([]byte, 10000)), false},
		{"whitespace-only transaction type", "   ", false},
		{"tab-only transaction type", "\t\t\t", false},
		{"newline-only transaction type", "\n\n\n", false},
		{"transaction type with leading/trailing spaces", "  payment  ", false},
		{"unicode characters in transaction type", "支付-تحويل-💰", false},
		{"special characters in transaction type", "payment@#$%^&*()", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lineage, err := NewTransactionLineage(uuid.New(), tt.transactionType, nil, nil, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTransactionLineage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && lineage == nil {
				t.Error("Expected lineage, got nil")
			}
			if !tt.wantErr && lineage.TransactionType() != tt.transactionType {
				t.Errorf("TransactionType() = %q, want %q", lineage.TransactionType(), tt.transactionType)
			}
		})
	}
}

func TestTransactionLineage_TransactionID(t *testing.T) {
	t.Parallel()

	txID := uuid.New()
	lineage, err := NewTransactionLineage(txID, "payment", nil, nil, nil)
	if err != nil {
		t.Fatalf("NewTransactionLineage() returned error: %v", err)
	}

	if lineage.TransactionID() != txID {
		t.Errorf("TransactionID() = %v, want %v", lineage.TransactionID(), txID)
	}
}

func TestTransactionLineage_ParentTransactionID(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when no parent", func(t *testing.T) {
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.ParentTransactionID()
		if result != nil {
			t.Errorf("ParentTransactionID() = %v, want nil", result)
		}
	})

	t.Run("returns copy of parent ID when parent exists", func(t *testing.T) {
		parentID := uuid.New()
		lineage, err := NewTransactionLineage(uuid.New(), "payment", &parentID, nil, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.ParentTransactionID()
		if result == nil {
			t.Fatal("ParentTransactionID() returned nil, expected non-nil")
		}
		if *result != parentID {
			t.Errorf("ParentTransactionID() = %v, want %v", *result, parentID)
		}
	})

	t.Run("defensive copy prevents mutation", func(t *testing.T) {
		parentID := uuid.New()
		lineage, err := NewTransactionLineage(uuid.New(), "payment", &parentID, nil, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		// Get parent and mutate it
		result := lineage.ParentTransactionID()
		*result = uuid.New()

		// Verify internal state unchanged
		secondResult := lineage.ParentTransactionID()
		if *secondResult != parentID {
			t.Errorf("Internal parent was mutated! Expected %v, got %v", parentID, *secondResult)
		}
	})
}

func TestTransactionLineage_ChildTransactionIDs(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when no children", func(t *testing.T) {
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.ChildTransactionIDs()
		if result != nil {
			t.Errorf("ChildTransactionIDs() = %v, want nil", result)
		}
	})

	t.Run("returns nil for empty slice", func(t *testing.T) {
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, []uuid.UUID{}, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.ChildTransactionIDs()
		if result != nil {
			t.Errorf("ChildTransactionIDs() = %v, want nil", result)
		}
	})

	t.Run("returns copy of children slice", func(t *testing.T) {
		child1 := uuid.New()
		child2 := uuid.New()
		children := []uuid.UUID{child1, child2}
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, children, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.ChildTransactionIDs()
		if len(result) != 2 {
			t.Fatalf("ChildTransactionIDs() returned %d children, want 2", len(result))
		}
		if result[0] != child1 || result[1] != child2 {
			t.Errorf("ChildTransactionIDs() = %v, want %v", result, children)
		}
	})

	t.Run("defensive copy prevents mutation", func(t *testing.T) {
		child1 := uuid.New()
		child2 := uuid.New()
		children := []uuid.UUID{child1, child2}
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, children, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		// Get children and mutate them
		result := lineage.ChildTransactionIDs()
		result[0] = uuid.New()
		_ = append(result, uuid.New())

		// Verify internal state unchanged
		secondResult := lineage.ChildTransactionIDs()
		if len(secondResult) != 2 {
			t.Errorf("Internal children slice was mutated! Expected length 2, got %d", len(secondResult))
		}
		if secondResult[0] != child1 || secondResult[1] != child2 {
			t.Errorf("Internal children were mutated! Expected [%v, %v], got %v", child1, child2, secondResult)
		}
	})
}

func TestTransactionLineage_RelatedTransactionIDs(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when no related transactions", func(t *testing.T) {
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, nil)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.RelatedTransactionIDs()
		if result != nil {
			t.Errorf("RelatedTransactionIDs() = %v, want nil", result)
		}
	})

	t.Run("returns nil for empty slice", func(t *testing.T) {
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, []uuid.UUID{})
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.RelatedTransactionIDs()
		if result != nil {
			t.Errorf("RelatedTransactionIDs() = %v, want nil", result)
		}
	})

	t.Run("returns copy of related slice", func(t *testing.T) {
		related1 := uuid.New()
		related2 := uuid.New()
		related := []uuid.UUID{related1, related2}
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, related)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		result := lineage.RelatedTransactionIDs()
		if len(result) != 2 {
			t.Fatalf("RelatedTransactionIDs() returned %d related, want 2", len(result))
		}
		if result[0] != related1 || result[1] != related2 {
			t.Errorf("RelatedTransactionIDs() = %v, want %v", result, related)
		}
	})

	t.Run("defensive copy prevents mutation", func(t *testing.T) {
		related1 := uuid.New()
		related2 := uuid.New()
		related := []uuid.UUID{related1, related2}
		lineage, err := NewTransactionLineage(uuid.New(), "payment", nil, nil, related)
		if err != nil {
			t.Fatalf("NewTransactionLineage() returned error: %v", err)
		}

		// Get related and mutate them
		result := lineage.RelatedTransactionIDs()
		result[0] = uuid.New()
		_ = append(result, uuid.New())

		// Verify internal state unchanged
		secondResult := lineage.RelatedTransactionIDs()
		if len(secondResult) != 2 {
			t.Errorf("Internal related slice was mutated! Expected length 2, got %d", len(secondResult))
		}
		if secondResult[0] != related1 || secondResult[1] != related2 {
			t.Errorf("Internal related were mutated! Expected [%v, %v], got %v", related1, related2, secondResult)
		}
	})
}
