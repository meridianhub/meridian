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
			name:    "whitespace-only UserID is currently accepted (no trimming)",
			userID:  "   ",
			action:  "ACTION",
			wantErr: nil,
		},
		{
			name:    "whitespace-only Action is currently accepted (no trimming)",
			userID:  "user123",
			action:  "   ",
			wantErr: nil,
		},
		{
			name:    "tab-only UserID is currently accepted (no trimming)",
			userID:  "\t\t",
			action:  "ACTION",
			wantErr: nil,
		},
		{
			name:    "newline-only Action is currently accepted (no trimming)",
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

func TestNewAuditTrailEntry_StringEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		userID      string
		action      string
		details     string
		ipAddress   string
		wantErr     bool
		expectedErr error
	}{
		{
			name:      "very long UserID (1000 chars)",
			userID:    strings.Repeat("U", 1000),
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "very long Action (1000 chars)",
			userID:    "user123",
			action:    strings.Repeat("A", 1000),
			details:   "Details",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "very long Details (100000 chars)",
			userID:    "user123",
			action:    "UPDATE",
			details:   strings.Repeat("D", 100000),
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "very long IPAddress (1000 chars) is accepted",
			userID:    "user123",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: strings.Repeat("1", 1000),
			wantErr:   false,
		},
		{
			name:      "empty Details is allowed (optional field)",
			userID:    "user123",
			action:    "UPDATE",
			details:   "",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "whitespace-only Details is allowed",
			userID:    "user123",
			action:    "UPDATE",
			details:   "   ",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "empty IPAddress is allowed (optional field)",
			userID:    "user123",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "",
			wantErr:   false,
		},
		{
			name:      "whitespace-only IPAddress is allowed",
			userID:    "user123",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "   ",
			wantErr:   false,
		},
		{
			name:      "UserID with leading/trailing spaces",
			userID:    "  user123  ",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "Action with leading/trailing spaces",
			userID:    "user123",
			action:    "  UPDATE  ",
			details:   "Details",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "unicode characters in UserID",
			userID:    "用户-123",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "unicode characters in Action",
			userID:    "user123",
			action:    "更新_ACCOUNT",
			details:   "Details",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "unicode characters in Details",
			userID:    "user123",
			action:    "UPDATE",
			details:   "更新账户余额 Updated account balance 💰",
			ipAddress: "192.168.1.1",
			wantErr:   false,
		},
		{
			name:      "special characters in IPAddress (IPv6)",
			userID:    "user123",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			wantErr:   false,
		},
		{
			name:      "invalid IPv4 format is accepted (no validation)",
			userID:    "user123",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "999.999.999.999",
			wantErr:   false,
		},
		{
			name:      "non-IP string in IPAddress is accepted",
			userID:    "user123",
			action:    "UPDATE",
			details:   "Details",
			ipAddress: "not-an-ip-address",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := NewAuditTrailEntry(tt.userID, tt.action, tt.details, tt.ipAddress, nil)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if entry.UserID != tt.userID {
				t.Errorf("Expected UserID to be preserved as %q, got %q", tt.userID, entry.UserID)
			}

			if entry.Action != tt.action {
				t.Errorf("Expected Action to be preserved as %q, got %q", tt.action, entry.Action)
			}

			if entry.Details != tt.details {
				t.Errorf("Expected Details to be preserved as %q, got %q", tt.details, entry.Details)
			}

			if entry.IPAddress != tt.ipAddress {
				t.Errorf("Expected IPAddress to be preserved as %q, got %q", tt.ipAddress, entry.IPAddress)
			}
		})
	}
}
