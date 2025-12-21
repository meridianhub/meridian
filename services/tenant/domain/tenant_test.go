package domain

import (
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func TestStatus_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{
			name:     "provisioning_pending is valid",
			status:   StatusProvisioningPending,
			expected: true,
		},
		{
			name:     "provisioning is valid",
			status:   StatusProvisioning,
			expected: true,
		},
		{
			name:     "provisioning_failed is valid",
			status:   StatusProvisioningFailed,
			expected: true,
		},
		{
			name:     "active is valid",
			status:   StatusActive,
			expected: true,
		},
		{
			name:     "suspended is valid",
			status:   StatusSuspended,
			expected: true,
		},
		{
			name:     "deprovisioned is valid",
			status:   StatusDeprovisioned,
			expected: true,
		},
		{
			name:     "invalid status returns false",
			status:   Status("invalid_status"),
			expected: false,
		},
		{
			name:     "empty status returns false",
			status:   Status(""),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.status.IsValid()
			if result != tt.expected {
				t.Errorf("Status(%q).IsValid() = %v, expected %v", tt.status, result, tt.expected)
			}
		})
	}
}

func TestTenant_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name            string
		currentStatus   Status
		targetStatus    Status
		expectedAllowed bool
	}{
		// StatusProvisioningPending transitions
		{
			name:            "provisioning_pending can transition to provisioning",
			currentStatus:   StatusProvisioningPending,
			targetStatus:    StatusProvisioning,
			expectedAllowed: true,
		},
		{
			name:            "provisioning_pending can transition to provisioning_failed",
			currentStatus:   StatusProvisioningPending,
			targetStatus:    StatusProvisioningFailed,
			expectedAllowed: true,
		},
		{
			name:            "provisioning_pending cannot transition to active",
			currentStatus:   StatusProvisioningPending,
			targetStatus:    StatusActive,
			expectedAllowed: false,
		},
		{
			name:            "provisioning_pending cannot transition to suspended",
			currentStatus:   StatusProvisioningPending,
			targetStatus:    StatusSuspended,
			expectedAllowed: false,
		},
		// StatusProvisioning transitions (existing behavior)
		{
			name:            "provisioning can transition to active",
			currentStatus:   StatusProvisioning,
			targetStatus:    StatusActive,
			expectedAllowed: true,
		},
		{
			name:            "provisioning can transition to provisioning_failed",
			currentStatus:   StatusProvisioning,
			targetStatus:    StatusProvisioningFailed,
			expectedAllowed: true,
		},
		// No-op transitions
		{
			name:            "no-op transition from provisioning_pending to itself",
			currentStatus:   StatusProvisioningPending,
			targetStatus:    StatusProvisioningPending,
			expectedAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenant := &Tenant{
				ID:     tenant.TenantID("test_tenant"),
				Status: tt.currentStatus,
			}
			result := tenant.CanTransitionTo(tt.targetStatus)
			if result != tt.expectedAllowed {
				t.Errorf("Tenant with status %q transitioning to %q: got %v, expected %v",
					tt.currentStatus, tt.targetStatus, result, tt.expectedAllowed)
			}
		})
	}
}

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		wantErr bool
		errMsg  string
	}{
		// Valid slugs
		{
			name:    "valid simple slug",
			slug:    "acme",
			wantErr: false,
		},
		{
			name:    "valid slug with numbers",
			slug:    "bank-123",
			wantErr: false,
		},
		{
			name:    "valid slug with multiple hyphens",
			slug:    "my-org",
			wantErr: false,
		},
		{
			name:    "valid slug all lowercase",
			slug:    "testcompany",
			wantErr: false,
		},
		{
			name:    "valid slug with numbers at start",
			slug:    "123bank",
			wantErr: false,
		},
		{
			name:    "valid slug minimum length",
			slug:    "abc",
			wantErr: false,
		},
		{
			name:    "valid slug maximum length",
			slug:    "a123456789012345678901234567890123456789012345678901234567890ab",
			wantErr: false,
		},
		{
			name:    "empty slug is valid",
			slug:    "",
			wantErr: false,
		},

		// Invalid formats
		{
			name:    "uppercase letters",
			slug:    "ACME",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "mixed case",
			slug:    "AcMeBaNk",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "leading hyphen",
			slug:    "-start",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "trailing hyphen",
			slug:    "end-",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "special characters",
			slug:    "special!",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "underscores",
			slug:    "with_underscore",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "spaces",
			slug:    "with space",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "dots",
			slug:    "with.dot",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},

		// Edge cases - length
		{
			name:    "too short - 2 chars",
			slug:    "ab",
			wantErr: true,
			errMsg:  "must be at least 3 characters long",
		},
		{
			name:    "too short - 1 char",
			slug:    "a",
			wantErr: true,
			errMsg:  "must be at least 3 characters long",
		},
		{
			name:    "too long - 64 chars",
			slug:    "a1234567890123456789012345678901234567890123456789012345678901234",
			wantErr: true,
			errMsg:  "must be at most 63 characters long",
		},

		// Reserved words
		{
			name:    "reserved - api",
			slug:    "api",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - admin",
			slug:    "admin",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - www",
			slug:    "www",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - health",
			slug:    "health",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - status",
			slug:    "status",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - docs",
			slug:    "docs",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - internal",
			slug:    "internal",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - system",
			slug:    "system",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - platform",
			slug:    "platform",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},

		// Edge cases - single character with hyphen
		{
			name:    "single hyphen",
			slug:    "-",
			wantErr: true,
			errMsg:  "must be at least 3 characters long",
		},
		{
			name:    "double hyphen",
			slug:    "a--b",
			wantErr: false, // Multiple consecutive hyphens are allowed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSlug(tt.slug)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateSlug(%q) expected error containing %q, got nil", tt.slug, tt.errMsg)
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("ValidateSlug(%q) error = %q, want error containing %q", tt.slug, err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateSlug(%q) unexpected error: %v", tt.slug, err)
				}
			}
		})
	}
}

// containsString checks if s contains substr (case-sensitive).
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
