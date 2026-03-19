package service

import (
	"errors"
	"testing"

	partyclient "github.com/meridianhub/meridian/services/party/client"
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

func TestSentinelErrors_AreDistinct(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrPartyRegistrationFailed", ErrPartyRegistrationFailed},
		{"ErrPartyServiceUnavailable", ErrPartyServiceUnavailable},
		{"ErrPartyServiceTimeout", ErrPartyServiceTimeout},
	}

	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a.err, b.err) {
				t.Errorf("%s should not match %s", a.name, b.name)
			}
		}
	}
}
