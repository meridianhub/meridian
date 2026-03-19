package domain

import (
	"strings"
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

func TestTenant_IsActive(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{"active tenant", StatusActive, true},
		{"suspended tenant", StatusSuspended, false},
		{"deprovisioned tenant", StatusDeprovisioned, false},
		{"provisioning tenant", StatusProvisioning, false},
		{"provisioning_pending tenant", StatusProvisioningPending, false},
		{"provisioning_failed tenant", StatusProvisioningFailed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantObj := &Tenant{ID: tenant.TenantID("test"), Status: tt.status}
			if got := tenantObj.IsActive(); got != tt.expected {
				t.Errorf("Tenant.IsActive() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTenant_CanOperate(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{"active can operate", StatusActive, true},
		{"suspended cannot operate", StatusSuspended, false},
		{"deprovisioned cannot operate", StatusDeprovisioned, false},
		{"provisioning cannot operate", StatusProvisioning, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantObj := &Tenant{ID: tenant.TenantID("test"), Status: tt.status}
			if got := tenantObj.CanOperate(); got != tt.expected {
				t.Errorf("Tenant.CanOperate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestTenant_SchemaName(t *testing.T) {
	tid, _ := tenant.NewTenantID("acme_bank")
	tenantObj := &Tenant{ID: tid}
	got := tenantObj.SchemaName()
	if got != "org_acme_bank" {
		t.Errorf("SchemaName() = %q, want %q", got, "org_acme_bank")
	}
}

func TestTenant_CanTransitionTo_AllPaths(t *testing.T) {
	// Test remaining transitions not covered by the existing test
	tests := []struct {
		name            string
		currentStatus   Status
		targetStatus    Status
		expectedAllowed bool
	}{
		// provisioning_failed transitions
		{"provisioning_failed can retry provisioning", StatusProvisioningFailed, StatusProvisioning, true},
		{"provisioning_failed cannot go to active", StatusProvisioningFailed, StatusActive, false},
		{"provisioning_failed cannot go to suspended", StatusProvisioningFailed, StatusSuspended, false},
		// active transitions
		{"active can be suspended", StatusActive, StatusSuspended, true},
		{"active can be deprovisioned", StatusActive, StatusDeprovisioned, true},
		{"active cannot go to provisioning", StatusActive, StatusProvisioning, false},
		// suspended transitions
		{"suspended can be reactivated", StatusSuspended, StatusActive, true},
		{"suspended can be deprovisioned", StatusSuspended, StatusDeprovisioned, true},
		{"suspended cannot go to provisioning", StatusSuspended, StatusProvisioning, false},
		// deprovisioned transitions
		{"deprovisioned is terminal", StatusDeprovisioned, StatusActive, false},
		{"deprovisioned cannot be suspended", StatusDeprovisioned, StatusSuspended, false},
		{"deprovisioned self-transition allowed", StatusDeprovisioned, StatusDeprovisioned, true},
		// unknown status
		{"unknown status returns false", Status("unknown"), StatusActive, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantObj := &Tenant{
				ID:     tenant.TenantID("test_tenant"),
				Status: tt.currentStatus,
			}
			result := tenantObj.CanTransitionTo(tt.targetStatus)
			if result != tt.expectedAllowed {
				t.Errorf("Tenant with status %q transitioning to %q: got %v, expected %v",
					tt.currentStatus, tt.targetStatus, result, tt.expectedAllowed)
			}
		})
	}
}

func TestServiceProvisioningStatus_IsValid(t *testing.T) {
	tests := []struct {
		status   ServiceProvisioningStatus
		expected bool
	}{
		{ServiceStatusPending, true},
		{ServiceStatusInProgress, true},
		{ServiceStatusCompleted, true},
		{ServiceStatusFailed, true},
		{ServiceProvisioningStatus("unknown"), false},
		{ServiceProvisioningStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.expected {
				t.Errorf("ServiceProvisioningStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.expected)
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
		{
			name:    "unicode characters - accented",
			slug:    "café",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "unicode characters - japanese",
			slug:    "日本",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "unicode characters - emoji",
			slug:    "test🚀",
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
		{
			name:    "reserved - app",
			slug:    "app",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - mail",
			slug:    "mail",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - cdn",
			slug:    "cdn",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - auth",
			slug:    "auth",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},
		{
			name:    "reserved - graphql",
			slug:    "graphql",
			wantErr: true,
			errMsg:  "is reserved and cannot be used",
		},

		// Edge cases - uppercase reserved words fail regex check first (not ErrSlugReserved)
		// This documents the validation order: length → format (regex) → reserved words
		{
			name:    "uppercase reserved word fails format check",
			slug:    "API",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
		},
		{
			name:    "mixed case reserved word fails format check",
			slug:    "Admin",
			wantErr: true,
			errMsg:  "must contain only lowercase alphanumeric characters and hyphens",
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
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
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
