package domain

import (
	"testing"
)

func TestTransactionStatus_IsValid(t *testing.T) {
	tests := []struct {
		name      string
		status    TransactionStatus
		wantValid bool
	}{
		{
			name:      "PENDING is valid",
			status:    TransactionStatusPending,
			wantValid: true,
		},
		{
			name:      "POSTED is valid",
			status:    TransactionStatusPosted,
			wantValid: true,
		},
		{
			name:      "FAILED is valid",
			status:    TransactionStatusFailed,
			wantValid: true,
		},
		{
			name:      "CANCELLED is valid",
			status:    TransactionStatusCancelled,
			wantValid: true,
		},
		{
			name:      "REVERSED is valid",
			status:    TransactionStatusReversed,
			wantValid: true,
		},
		{
			name:      "empty string is invalid",
			status:    TransactionStatus(""),
			wantValid: false,
		},
		{
			name:      "unknown status is invalid",
			status:    TransactionStatus("UNKNOWN"),
			wantValid: false,
		},
		{
			name:      "lowercase pending is invalid",
			status:    TransactionStatus("pending"),
			wantValid: false,
		},
		{
			name:      "ACTIVE is invalid",
			status:    TransactionStatus("ACTIVE"),
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.wantValid {
				t.Errorf("TransactionStatus.IsValid() = %v, want %v", got, tt.wantValid)
			}
		})
	}
}

func TestTransactionStatus_String(t *testing.T) {
	tests := []struct {
		name   string
		status TransactionStatus
		want   string
	}{
		{
			name:   "PENDING string",
			status: TransactionStatusPending,
			want:   "PENDING",
		},
		{
			name:   "POSTED string",
			status: TransactionStatusPosted,
			want:   "POSTED",
		},
		{
			name:   "FAILED string",
			status: TransactionStatusFailed,
			want:   "FAILED",
		},
		{
			name:   "CANCELLED string",
			status: TransactionStatusCancelled,
			want:   "CANCELLED",
		},
		{
			name:   "REVERSED string",
			status: TransactionStatusReversed,
			want:   "REVERSED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("TransactionStatus.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTransactionStatus_IsFinal(t *testing.T) {
	tests := []struct {
		name      string
		status    TransactionStatus
		wantFinal bool
	}{
		{
			name:      "PENDING is not final",
			status:    TransactionStatusPending,
			wantFinal: false,
		},
		{
			name:      "POSTED is final",
			status:    TransactionStatusPosted,
			wantFinal: true,
		},
		{
			name:      "FAILED is final",
			status:    TransactionStatusFailed,
			wantFinal: true,
		},
		{
			name:      "CANCELLED is final",
			status:    TransactionStatusCancelled,
			wantFinal: true,
		},
		{
			name:      "REVERSED is final",
			status:    TransactionStatusReversed,
			wantFinal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsFinal(); got != tt.wantFinal {
				t.Errorf("TransactionStatus.IsFinal() = %v, want %v", got, tt.wantFinal)
			}
		})
	}
}
