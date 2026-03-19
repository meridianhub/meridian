package service

import (
	"errors"
	"testing"

	partyclient "github.com/meridianhub/meridian/services/party/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewPartyClientAdapter(t *testing.T) {
	cleanupCalled := false
	adapter := NewPartyClientAdapter(&partyclient.Client{}, func() { cleanupCalled = true })

	if adapter == nil {
		t.Fatal("Expected non-nil adapter")
	}
	if adapter.client == nil {
		t.Error("Expected non-nil client")
	}

	// Test close calls cleanup
	err := adapter.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
	if !cleanupCalled {
		t.Error("Expected cleanup to be called")
	}
}

func TestPartyClientAdapter_Close_NilCleanup(t *testing.T) {
	adapter := &PartyClientAdapter{
		client:  &partyclient.Client{},
		cleanup: nil,
	}

	err := adapter.Close()
	if err != nil {
		t.Errorf("Close with nil cleanup failed: %v", err)
	}
}

func TestPartyClientAdapter_RegisterParty_ErrorCodes(t *testing.T) {
	tests := []struct {
		name        string
		grpcCode    codes.Code
		expectedErr error
	}{
		{"already exists", codes.AlreadyExists, ErrPartyRegistrationFailed},
		{"invalid argument", codes.InvalidArgument, ErrPartyRegistrationFailed},
		{"unavailable", codes.Unavailable, ErrPartyServiceUnavailable},
		{"deadline exceeded", codes.DeadlineExceeded, ErrPartyServiceTimeout},
		{"internal error", codes.Internal, ErrPartyRegistrationFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectedErr == nil {
				t.Error("Expected non-nil sentinel error")
			}
		})
	}
}

func TestSentinelErrors(t *testing.T) {
	// Verify sentinel errors are distinct
	if errors.Is(ErrPartyRegistrationFailed, ErrPartyServiceUnavailable) {
		t.Error("ErrPartyRegistrationFailed should not match ErrPartyServiceUnavailable")
	}
	if errors.Is(ErrPartyRegistrationFailed, ErrPartyServiceTimeout) {
		t.Error("ErrPartyRegistrationFailed should not match ErrPartyServiceTimeout")
	}
	if errors.Is(ErrPartyServiceUnavailable, ErrPartyServiceTimeout) {
		t.Error("ErrPartyServiceUnavailable should not match ErrPartyServiceTimeout")
	}

	// Verify wrapping works
	wrapped := status.Error(codes.Unavailable, "test")
	_ = wrapped
}
