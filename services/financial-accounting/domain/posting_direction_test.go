package domain

import (
	"testing"
)

func TestPostingDirection_IsValid(t *testing.T) {
	tests := []struct {
		name      string
		direction PostingDirection
		wantValid bool
	}{
		{
			name:      "DEBIT is valid",
			direction: PostingDirectionDebit,
			wantValid: true,
		},
		{
			name:      "CREDIT is valid",
			direction: PostingDirectionCredit,
			wantValid: true,
		},
		{
			name:      "empty string is invalid",
			direction: PostingDirection(""),
			wantValid: false,
		},
		{
			name:      "unknown direction is invalid",
			direction: PostingDirection("UNKNOWN"),
			wantValid: false,
		},
		{
			name:      "lowercase debit is invalid",
			direction: PostingDirection("debit"),
			wantValid: false,
		},
		{
			name:      "lowercase credit is invalid",
			direction: PostingDirection("credit"),
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.direction.IsValid(); got != tt.wantValid {
				t.Errorf("PostingDirection.IsValid() = %v, want %v", got, tt.wantValid)
			}
		})
	}
}

func TestPostingDirection_String(t *testing.T) {
	tests := []struct {
		name      string
		direction PostingDirection
		want      string
	}{
		{
			name:      "DEBIT string",
			direction: PostingDirectionDebit,
			want:      "DEBIT",
		},
		{
			name:      "CREDIT string",
			direction: PostingDirectionCredit,
			want:      "CREDIT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.direction.String(); got != tt.want {
				t.Errorf("PostingDirection.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPostingDirection_Opposite(t *testing.T) {
	tests := []struct {
		name      string
		direction PostingDirection
		want      PostingDirection
	}{
		{
			name:      "opposite of DEBIT is CREDIT",
			direction: PostingDirectionDebit,
			want:      PostingDirectionCredit,
		},
		{
			name:      "opposite of CREDIT is DEBIT",
			direction: PostingDirectionCredit,
			want:      PostingDirectionDebit,
		},
		{
			name:      "opposite of empty defaults to DEBIT",
			direction: PostingDirection(""),
			want:      PostingDirectionDebit,
		},
		{
			name:      "opposite of unknown defaults to DEBIT",
			direction: PostingDirection("UNKNOWN"),
			want:      PostingDirectionDebit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.direction.Opposite(); got != tt.want {
				t.Errorf("PostingDirection.Opposite() = %v, want %v", got, tt.want)
			}
		})
	}
}
