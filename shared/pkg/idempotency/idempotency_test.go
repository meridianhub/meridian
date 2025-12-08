package idempotency

import (
	"errors"
	"testing"
	"time"
)

func TestKey_String(t *testing.T) {
	tests := []struct {
		name string
		key  Key
		want string
	}{
		{
			name: "full key with request ID (no org)",
			key: Key{
				Namespace: "current-account",
				Operation: "deposit",
				EntityID:  "ACC-12345",
				RequestID: "req-abc",
			},
			want: "idempotency:current-account:deposit:ACC-12345:req-abc",
		},
		{
			name: "key without request ID (no org)",
			key: Key{
				Namespace: "current-account",
				Operation: "withdraw",
				EntityID:  "ACC-67890",
			},
			want: "idempotency:current-account:withdraw:ACC-67890",
		},
		{
			name: "financial accounting namespace (no org)",
			key: Key{
				Namespace: "financial-accounting",
				Operation: "post-ledger",
				EntityID:  "TXN-999",
				RequestID: "idempotency-token-123",
			},
			want: "idempotency:financial-accounting:post-ledger:TXN-999:idempotency-token-123",
		},
		{
			name: "with organization ID and request ID",
			key: Key{
				OrganizationID: "acme_bank",
				Namespace:      "payment-order",
				Operation:      "create",
				EntityID:       "payment-123",
				RequestID:      "req-456",
			},
			want: "acme_bank:idempotency:payment-order:create:payment-123:req-456",
		},
		{
			name: "with organization ID without request ID",
			key: Key{
				OrganizationID: "acme_bank",
				Namespace:      "current-account",
				Operation:      "deposit",
				EntityID:       "ACC-12345",
			},
			want: "acme_bank:idempotency:current-account:deposit:ACC-12345",
		},
		{
			name: "different organizations same operation",
			key: Key{
				OrganizationID: "other_bank",
				Namespace:      "payment-order",
				Operation:      "create",
				EntityID:       "payment-123",
				RequestID:      "req-456",
			},
			want: "other_bank:idempotency:payment-order:create:payment-123:req-456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.key.String()
			if got != tt.want {
				t.Errorf("Key.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKey_String_OrganizationIsolation(t *testing.T) {
	// Same operation across different organizations should produce different keys
	keyOrg1 := Key{
		OrganizationID: "org_alpha",
		Namespace:      "payment",
		Operation:      "transfer",
		EntityID:       "TXN-001",
		RequestID:      "same-request-id",
	}

	keyOrg2 := Key{
		OrganizationID: "org_beta",
		Namespace:      "payment",
		Operation:      "transfer",
		EntityID:       "TXN-001",
		RequestID:      "same-request-id",
	}

	if keyOrg1.String() == keyOrg2.String() {
		t.Error("Keys with different organizations should produce different strings")
	}

	// Verify the keys contain the organization prefix
	if keyOrg1.String() != "org_alpha:idempotency:payment:transfer:TXN-001:same-request-id" {
		t.Errorf("Unexpected key format: %s", keyOrg1.String())
	}
	if keyOrg2.String() != "org_beta:idempotency:payment:transfer:TXN-001:same-request-id" {
		t.Errorf("Unexpected key format: %s", keyOrg2.String())
	}
}

func TestKey_Validate(t *testing.T) {
	tests := []struct {
		name    string
		key     Key
		wantErr bool
	}{
		{
			name: "valid key with all fields",
			key: Key{
				Namespace: "current-account",
				Operation: "deposit",
				EntityID:  "ACC-12345",
				RequestID: "req-abc",
			},
			wantErr: false,
		},
		{
			name: "valid key without request ID",
			key: Key{
				Namespace: "current-account",
				Operation: "deposit",
				EntityID:  "ACC-12345",
			},
			wantErr: false,
		},
		{
			name: "missing namespace",
			key: Key{
				Operation: "deposit",
				EntityID:  "ACC-12345",
			},
			wantErr: true,
		},
		{
			name: "missing operation",
			key: Key{
				Namespace: "current-account",
				EntityID:  "ACC-12345",
			},
			wantErr: true,
		},
		{
			name: "missing entity ID",
			key: Key{
				Namespace: "current-account",
				Operation: "deposit",
			},
			wantErr: true,
		},
		{
			name:    "empty key",
			key:     Key{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Key.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, ErrInvalidKey) {
				t.Errorf("Key.Validate() error = %v, want %v", err, ErrInvalidKey)
			}
		})
	}
}

func TestDefaultLockOptions(t *testing.T) {
	opts := DefaultLockOptions()

	if opts.TTL != 30*time.Second {
		t.Errorf("DefaultLockOptions().TTL = %v, want %v", opts.TTL, 30*time.Second)
	}
	if opts.RetryDelay != 100*time.Millisecond {
		t.Errorf("DefaultLockOptions().RetryDelay = %v, want %v", opts.RetryDelay, 100*time.Millisecond)
	}
	if opts.MaxRetries != 3 {
		t.Errorf("DefaultLockOptions().MaxRetries = %v, want %v", opts.MaxRetries, 3)
	}
}

func TestOperationStatus(t *testing.T) {
	// Verify status constants are distinct
	statuses := map[OperationStatus]bool{
		StatusPending:   true,
		StatusCompleted: true,
		StatusFailed:    true,
	}

	if len(statuses) != 3 {
		t.Errorf("Expected 3 distinct status values, got %d", len(statuses))
	}

	// Verify status values are non-empty strings
	if string(StatusPending) == "" {
		t.Error("StatusPending should not be empty")
	}
	if string(StatusCompleted) == "" {
		t.Error("StatusCompleted should not be empty")
	}
	if string(StatusFailed) == "" {
		t.Error("StatusFailed should not be empty")
	}
}

func TestResult_Structure(t *testing.T) {
	// Test that Result can be constructed with all fields
	now := time.Now()
	result := Result{
		Key: Key{
			Namespace: "test",
			Operation: "test-op",
			EntityID:  "test-123",
		},
		Status:      StatusCompleted,
		Data:        []byte(`{"result": "success"}`),
		Error:       "",
		CompletedAt: now,
		TTL:         24 * time.Hour,
	}

	if result.Status != StatusCompleted {
		t.Errorf("Result.Status = %v, want %v", result.Status, StatusCompleted)
	}
	if string(result.Data) != `{"result": "success"}` {
		t.Errorf("Result.Data = %v, want %v", string(result.Data), `{"result": "success"}`)
	}
	if result.TTL != 24*time.Hour {
		t.Errorf("Result.TTL = %v, want %v", result.TTL, 24*time.Hour)
	}
}

func TestErrors_AreDistinct(t *testing.T) {
	// Verify all errors are distinct
	errors := []error{
		ErrOperationAlreadyProcessed,
		ErrLockAcquisitionFailed,
		ErrLockNotHeld,
		ErrInvalidKey,
		ErrResultNotFound,
		ErrEmptyToken,
		ErrInvalidTTL,
		ErrInvalidStatus,
		ErrUnexpectedRedisResponse,
	}

	errorSet := make(map[string]bool)
	for _, err := range errors {
		msg := err.Error()
		if errorSet[msg] {
			t.Errorf("Duplicate error message: %s", msg)
		}
		errorSet[msg] = true
	}

	if len(errorSet) != 9 {
		t.Errorf("Expected 9 distinct errors, got %d", len(errorSet))
	}
}

func TestKey_Validate_RejectsColons(t *testing.T) {
	tests := []struct {
		name string
		key  Key
	}{
		{
			name: "colon in organization ID",
			key: Key{
				OrganizationID: "org:id",
				Namespace:      "current-account",
				Operation:      "deposit",
				EntityID:       "ACC-123",
			},
		},
		{
			name: "colon in namespace",
			key: Key{
				Namespace: "name:space",
				Operation: "deposit",
				EntityID:  "ACC-123",
			},
		},
		{
			name: "colon in operation",
			key: Key{
				Namespace: "current-account",
				Operation: "dep:osit",
				EntityID:  "ACC-123",
			},
		},
		{
			name: "colon in entity ID",
			key: Key{
				Namespace: "current-account",
				Operation: "deposit",
				EntityID:  "ACC:123",
			},
		},
		{
			name: "colon in request ID",
			key: Key{
				Namespace: "current-account",
				Operation: "deposit",
				EntityID:  "ACC-123",
				RequestID: "req:abc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.Validate()
			if err == nil {
				t.Errorf("Key.Validate() expected error for key with colon, got nil")
			}
			if !errors.Is(err, ErrInvalidKey) {
				t.Errorf("Key.Validate() error = %v, want %v", err, ErrInvalidKey)
			}
		})
	}
}

func TestKey_Validate_WithOrganizationID(t *testing.T) {
	tests := []struct {
		name    string
		key     Key
		wantErr bool
	}{
		{
			name: "valid key with organization ID",
			key: Key{
				OrganizationID: "acme_bank",
				Namespace:      "current-account",
				Operation:      "deposit",
				EntityID:       "ACC-12345",
				RequestID:      "req-abc",
			},
			wantErr: false,
		},
		{
			name: "valid key with organization ID, no request ID",
			key: Key{
				OrganizationID: "acme_bank",
				Namespace:      "current-account",
				Operation:      "deposit",
				EntityID:       "ACC-12345",
			},
			wantErr: false,
		},
		{
			name: "organization ID is optional (empty is valid)",
			key: Key{
				OrganizationID: "",
				Namespace:      "current-account",
				Operation:      "deposit",
				EntityID:       "ACC-12345",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Key.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
