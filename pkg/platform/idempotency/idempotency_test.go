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
			name: "full key with request ID",
			key: Key{
				Namespace: "current-account",
				Operation: "deposit",
				EntityID:  "ACC-12345",
				RequestID: "req-abc",
			},
			want: "current-account:deposit:ACC-12345:req-abc",
		},
		{
			name: "key without request ID",
			key: Key{
				Namespace: "current-account",
				Operation: "withdraw",
				EntityID:  "ACC-67890",
			},
			want: "current-account:withdraw:ACC-67890",
		},
		{
			name: "financial accounting namespace",
			key: Key{
				Namespace: "financial-accounting",
				Operation: "post-ledger",
				EntityID:  "TXN-999",
				RequestID: "idempotency-token-123",
			},
			want: "financial-accounting:post-ledger:TXN-999:idempotency-token-123",
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
	}

	errorSet := make(map[string]bool)
	for _, err := range errors {
		msg := err.Error()
		if errorSet[msg] {
			t.Errorf("Duplicate error message: %s", msg)
		}
		errorSet[msg] = true
	}

	if len(errorSet) != 7 {
		t.Errorf("Expected 7 distinct errors, got %d", len(errorSet))
	}
}
