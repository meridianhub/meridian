package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const testServiceName = "position-keeping"

func TestNewAuditTrailEntry_ValidInputs(t *testing.T) {
	t.Parallel()

	userID := "user123"
	action := "UPDATE_ACCOUNT"
	details := "Updated account balance"
	ipAddress := "192.168.1.1"
	systemContext := map[string]string{
		"service": testServiceName,
		"version": "1.0",
	}

	entry, err := NewAuditTrailEntry(userID, action, details, ipAddress, systemContext)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if entry.AuditID == uuid.Nil {
		t.Error("Expected non-nil AuditID")
	}

	if entry.UserID != userID {
		t.Errorf("Expected UserID %v, got %v", userID, entry.UserID)
	}

	if entry.Action != action {
		t.Errorf("Expected Action %v, got %v", action, entry.Action)
	}

	if entry.Details != details {
		t.Errorf("Expected Details %v, got %v", details, entry.Details)
	}

	if entry.IPAddress != ipAddress {
		t.Errorf("Expected IPAddress %v, got %v", ipAddress, entry.IPAddress)
	}

	if entry.SystemContext["service"] != testServiceName {
		t.Error("Expected SystemContext to contain service key")
	}

	if entry.SystemContext["version"] != "1.0" {
		t.Error("Expected SystemContext to contain version key")
	}
}

func TestNewAuditTrailEntry_EmptyUserID(t *testing.T) {
	t.Parallel()

	_, err := NewAuditTrailEntry("", "ACTION", "details", "192.168.1.1", nil)

	if !errors.Is(err, ErrInvalidUserID) {
		t.Errorf("Expected ErrInvalidUserID, got: %v", err)
	}
}

func TestNewAuditTrailEntry_EmptyAction(t *testing.T) {
	t.Parallel()

	_, err := NewAuditTrailEntry("user123", "", "details", "192.168.1.1", nil)

	if !errors.Is(err, ErrInvalidAction) {
		t.Errorf("Expected ErrInvalidAction, got: %v", err)
	}
}

func TestNewAuditTrailEntry_NilSystemContext(t *testing.T) {
	t.Parallel()

	entry, err := NewAuditTrailEntry("user123", "ACTION", "details", "192.168.1.1", nil)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if entry.SystemContext == nil {
		t.Error("Expected SystemContext to be initialized to empty map, got nil")
	}

	if len(entry.SystemContext) != 0 {
		t.Errorf("Expected SystemContext to be empty, got %d entries", len(entry.SystemContext))
	}
}

func TestNewAuditTrailEntry_PopulatedSystemContext(t *testing.T) {
	t.Parallel()

	systemContext := map[string]string{
		"service":   testServiceName,
		"version":   "1.0",
		"requestID": "req-12345",
	}

	entry, err := NewAuditTrailEntry("user123", "ACTION", "details", "192.168.1.1", systemContext)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if len(entry.SystemContext) != 3 {
		t.Errorf("Expected SystemContext to have 3 entries, got %d", len(entry.SystemContext))
	}

	if entry.SystemContext["service"] != testServiceName {
		t.Error("Expected SystemContext to contain service key")
	}

	if entry.SystemContext["version"] != "1.0" {
		t.Error("Expected SystemContext to contain version key")
	}

	if entry.SystemContext["requestID"] != "req-12345" {
		t.Error("Expected SystemContext to contain requestID key")
	}
}

func TestNewAuditTrailEntry_SystemContextImmutability(t *testing.T) {
	t.Parallel()

	originalContext := map[string]string{
		"service": testServiceName,
		"version": "1.0",
	}

	entry, err := NewAuditTrailEntry("user123", "ACTION", "details", "192.168.1.1", originalContext)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Modify the original map
	originalContext["modified"] = "true"
	delete(originalContext, "service")

	// Verify the entry's SystemContext is unchanged
	if _, exists := entry.SystemContext["modified"]; exists {
		t.Error("Expected SystemContext to be immutable, but modification was reflected")
	}

	if entry.SystemContext["service"] != testServiceName {
		t.Error("Expected SystemContext to be immutable, but deletion was reflected")
	}
}

func TestNewAuditTrailEntry_AuditIDGeneration(t *testing.T) {
	t.Parallel()

	entry1, err1 := NewAuditTrailEntry("user123", "ACTION1", "details", "192.168.1.1", nil)
	entry2, err2 := NewAuditTrailEntry("user123", "ACTION2", "details", "192.168.1.1", nil)

	if err1 != nil || err2 != nil {
		t.Fatalf("Expected no errors, got: %v, %v", err1, err2)
	}

	if entry1.AuditID == uuid.Nil {
		t.Error("Expected entry1 AuditID to not be uuid.Nil")
	}

	if entry2.AuditID == uuid.Nil {
		t.Error("Expected entry2 AuditID to not be uuid.Nil")
	}

	if entry1.AuditID == entry2.AuditID {
		t.Error("Expected AuditIDs to be unique")
	}
}

func TestNewAuditTrailEntry_TimestampIsUTCAndRecent(t *testing.T) {
	t.Parallel()

	before := time.Now().UTC()
	entry, err := NewAuditTrailEntry("user123", "ACTION", "details", "192.168.1.1", nil)
	after := time.Now().UTC()

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if entry.Timestamp.Location() != time.UTC {
		t.Errorf("Expected Timestamp to be UTC, got: %v", entry.Timestamp.Location())
	}

	if entry.Timestamp.Before(before) || entry.Timestamp.After(after) {
		t.Errorf("Expected Timestamp to be between %v and %v, got: %v", before, after, entry.Timestamp)
	}
}

func TestNewAuditTrailEntry_VeryLongStrings(t *testing.T) {
	t.Parallel()

	longUserID := strings.Repeat("a", 1000)
	longAction := strings.Repeat("b", 1000)

	entry, err := NewAuditTrailEntry(longUserID, longAction, "details", "192.168.1.1", nil)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	if entry.UserID != longUserID {
		t.Error("Expected UserID to be preserved")
	}

	if entry.Action != longAction {
		t.Error("Expected Action to be preserved")
	}
}

func TestNewAuditTrailEntry_WhitespaceOnlyStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		userID  string
		action  string
		wantErr error
	}{
		{
			name:    "whitespace-only UserID",
			userID:  "   ",
			action:  "ACTION",
			wantErr: nil, // Whitespace is technically not empty
		},
		{
			name:    "whitespace-only Action",
			userID:  "user123",
			action:  "   ",
			wantErr: nil, // Whitespace is technically not empty
		},
		{
			name:    "tab-only UserID",
			userID:  "\t\t",
			action:  "ACTION",
			wantErr: nil,
		},
		{
			name:    "newline-only Action",
			userID:  "user123",
			action:  "\n\n",
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAuditTrailEntry(tt.userID, tt.action, "details", "192.168.1.1", nil)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Expected error %v, got: %v", tt.wantErr, err)
			}
		})
	}
}
