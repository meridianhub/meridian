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
