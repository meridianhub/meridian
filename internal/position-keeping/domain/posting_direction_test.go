package domain

import (
	"testing"
)

func TestPostingDirection_IsValid_Debit(t *testing.T) {
	t.Parallel()

	if !PostingDirectionDebit.IsValid() {
		t.Error("Expected DEBIT to be valid")
	}
}

func TestPostingDirection_IsValid_Credit(t *testing.T) {
	t.Parallel()

	if !PostingDirectionCredit.IsValid() {
		t.Error("Expected CREDIT to be valid")
	}
}

func TestPostingDirection_IsValid_EmptyString(t *testing.T) {
	t.Parallel()

	empty := PostingDirection("")
	if empty.IsValid() {
		t.Error("Expected empty string to be invalid")
	}
}

func TestPostingDirection_IsValid_InvalidValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		direction PostingDirection
	}{
		{"lowercase debit", PostingDirection("debit")},
		{"lowercase credit", PostingDirection("credit")},
		{"invalid string", PostingDirection("INVALID")},
		{"numeric", PostingDirection("123")},
		{"special chars", PostingDirection("@#$")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.direction.IsValid() {
				t.Errorf("Expected %v to be invalid", tt.direction)
			}
		})
	}
}

func TestPostingDirection_Opposite_Debit(t *testing.T) {
	t.Parallel()

	opposite := PostingDirectionDebit.Opposite()

	if opposite != PostingDirectionCredit {
		t.Errorf("Expected opposite of DEBIT to be CREDIT, got: %v", opposite)
	}
}

func TestPostingDirection_Opposite_Credit(t *testing.T) {
	t.Parallel()

	opposite := PostingDirectionCredit.Opposite()

	if opposite != PostingDirectionDebit {
		t.Errorf("Expected opposite of CREDIT to be DEBIT, got: %v", opposite)
	}
}

func TestPostingDirection_Opposite_InvalidValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		direction PostingDirection
		expected  PostingDirection
	}{
		{"empty string", PostingDirection(""), PostingDirectionDebit},
		{"invalid string", PostingDirection("INVALID"), PostingDirectionDebit},
		{"lowercase debit", PostingDirection("debit"), PostingDirectionDebit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opposite := tt.direction.Opposite()

			if opposite != tt.expected {
				t.Errorf("Expected opposite of %v to be %v, got: %v", tt.direction, tt.expected, opposite)
			}
		})
	}
}

func TestPostingDirection_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		direction PostingDirection
		expected  string
	}{
		{"debit", PostingDirectionDebit, "DEBIT"},
		{"credit", PostingDirectionCredit, "CREDIT"},
		{"empty", PostingDirection(""), ""},
		{"invalid", PostingDirection("INVALID"), "INVALID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.direction.String()
			if result != tt.expected {
				t.Errorf("Expected String() to return %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestParsePostingDirection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected PostingDirection
	}{
		{"valid debit", "DEBIT", PostingDirectionDebit},
		{"valid credit", "CREDIT", PostingDirectionCredit},
		{"invalid defaults to debit", "INVALID", PostingDirectionDebit},
		{"empty defaults to debit", "", PostingDirectionDebit},
		{"lowercase defaults to debit", "debit", PostingDirectionDebit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParsePostingDirection(tt.input)
			if result != tt.expected {
				t.Errorf("Expected ParsePostingDirection(%q) to return %v, got %v", tt.input, tt.expected, result)
			}
		})
	}
}
