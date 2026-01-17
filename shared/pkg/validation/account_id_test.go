package validation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateAccountID(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		wantErr   error
	}{
		// Valid cases
		{"valid UUID style", "550e8400-e29b-41d4-a716-446655440000", nil},
		{"valid alphanumeric", "ACC123", nil},
		{"valid with underscore", "CLR_GBP_001", nil},
		{"valid with hyphen", "CLR-GBP-001", nil},
		{"valid mixed", "Account-123_v2", nil},
		{"single character", "A", nil},
		{"100 characters", strings.Repeat("a", 100), nil},
		{"pure numbers", "12345", nil},
		{"lowercase only", "account", nil},
		{"uppercase only", "ACCOUNT", nil},

		// Invalid cases
		{"empty string", "", ErrAccountIDRequired},
		{"exceeds max length", strings.Repeat("a", 101), ErrAccountIDTooLong},
		{"contains space", "ACC 123", ErrAccountIDInvalidCharacters},
		{"contains at symbol", "ACC@123", ErrAccountIDInvalidCharacters},
		{"contains slash", "ACC/123", ErrAccountIDInvalidCharacters},
		{"contains hash", "ACC#123", ErrAccountIDInvalidCharacters},
		{"contains dollar", "ACC$123", ErrAccountIDInvalidCharacters},
		{"only special chars", "!@#$", ErrAccountIDInvalidCharacters},
		{"contains dot", "ACC.123", ErrAccountIDInvalidCharacters},
		{"contains newline", "ACC\n123", ErrAccountIDInvalidCharacters},
		{"contains tab", "ACC\t123", ErrAccountIDInvalidCharacters},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAccountID(tt.accountID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func BenchmarkValidateAccountID(b *testing.B) {
	validID := "CLR-GBP-001"
	for i := 0; i < b.N; i++ {
		_ = ValidateAccountID(validID)
	}
}
