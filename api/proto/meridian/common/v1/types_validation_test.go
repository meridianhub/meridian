package commonv1_test

import (
	"testing"

	"buf.build/go/protovalidate"
	commonv1 "github.com/bjcoombs/meridian/api/proto/meridian/common/v1"
)

// TestValidation_IdempotencyKeyPattern tests idempotency key pattern validation
func TestValidation_IdempotencyKeyPattern(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name      string
		key       string
		wantError bool
	}{
		{
			name:      "valid UUID v4",
			key:       "550e8400-e29b-41d4-a716-446655440000",
			wantError: false,
		},
		{
			name:      "valid alphanumeric",
			key:       "request123456",
			wantError: false,
		},
		{
			name:      "valid with hyphens",
			key:       "request-123-456",
			wantError: false,
		},
		{
			name:      "valid with underscores",
			key:       "request_123_456",
			wantError: false,
		},
		{
			name:      "valid mixed",
			key:       "req-2024_01-abc123",
			wantError: false,
		},
		{
			name:      "invalid with spaces",
			key:       "request 123",
			wantError: true,
		},
		{
			name:      "invalid with special chars",
			key:       "request@123",
			wantError: true,
		},
		{
			name:      "invalid with slashes",
			key:       "request/123",
			wantError: true,
		},
		{
			name:      "invalid with dots",
			key:       "request.123",
			wantError: true,
		},
		{
			name:      "invalid with colons",
			key:       "request:123",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idempotencyKey := &commonv1.IdempotencyKey{
				Key:        tt.key,
				TtlSeconds: 3600,
			}

			err := validator.Validate(idempotencyKey)
			if tt.wantError {
				if err == nil {
					t.Errorf("Expected validation error for key %q but got none", tt.key)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error for key %q: %v", tt.key, err)
				}
			}
		})
	}
}

// TestValidation_IdempotencyKeyLength tests key length validation
func TestValidation_IdempotencyKeyLength(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	tests := []struct {
		name      string
		key       string
		wantError bool
	}{
		{
			name:      "valid minimum length (1 char)",
			key:       "a",
			wantError: false,
		},
		{
			name:      "valid typical length",
			key:       "request-123-abc",
			wantError: false,
		},
		{
			name:      "valid maximum length (255 chars)",
			key:       "a12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234",
			wantError: false,
		},
		{
			name:      "invalid empty key",
			key:       "",
			wantError: true,
		},
		{
			name:      "invalid too long (256 chars)",
			key:       "a1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idempotencyKey := &commonv1.IdempotencyKey{
				Key:        tt.key,
				TtlSeconds: 3600,
			}

			err := validator.Validate(idempotencyKey)
			if tt.wantError {
				if err == nil {
					t.Errorf("Expected validation error for key length %d but got none", len(tt.key))
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error for key length %d: %v", len(tt.key), err)
				}
			}
		})
	}
}

// TestValidation_MoneyAmountRequired tests that MoneyAmount amount field is required
func TestValidation_MoneyAmountRequired(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("Failed to create validator: %v", err)
	}

	t.Run("missing amount field", func(t *testing.T) {
		moneyAmount := &commonv1.MoneyAmount{
			Amount: nil, // Missing required field
		}

		err := validator.Validate(moneyAmount)
		if err == nil {
			t.Error("Expected validation error for missing amount field but got none")
		}
	})
}
