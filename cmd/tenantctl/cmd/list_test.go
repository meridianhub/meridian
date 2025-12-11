package cmd

import (
	"testing"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/stretchr/testify/assert"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected tenantv1.TenantStatus
		wantErr  bool
	}{
		{
			name:     "active lowercase",
			input:    "active",
			expected: tenantv1.TenantStatus_TENANT_STATUS_ACTIVE,
			wantErr:  false,
		},
		{
			name:     "active uppercase",
			input:    "ACTIVE",
			expected: tenantv1.TenantStatus_TENANT_STATUS_ACTIVE,
			wantErr:  false,
		},
		{
			name:     "active mixed case",
			input:    "Active",
			expected: tenantv1.TenantStatus_TENANT_STATUS_ACTIVE,
			wantErr:  false,
		},
		{
			name:     "suspended",
			input:    "suspended",
			expected: tenantv1.TenantStatus_TENANT_STATUS_SUSPENDED,
			wantErr:  false,
		},
		{
			name:     "deprovisioned",
			input:    "deprovisioned",
			expected: tenantv1.TenantStatus_TENANT_STATUS_DEPROVISIONED,
			wantErr:  false,
		},
		{
			name:     "invalid status",
			input:    "invalid",
			expected: tenantv1.TenantStatus_TENANT_STATUS_UNSPECIFIED,
			wantErr:  true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: tenantv1.TenantStatus_TENANT_STATUS_UNSPECIFIED,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseStatus(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidStatus)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    tenantv1.TenantStatus
		expected string
	}{
		{
			name:     "active",
			input:    tenantv1.TenantStatus_TENANT_STATUS_ACTIVE,
			expected: "active",
		},
		{
			name:     "suspended",
			input:    tenantv1.TenantStatus_TENANT_STATUS_SUSPENDED,
			expected: "suspended",
		},
		{
			name:     "deprovisioned",
			input:    tenantv1.TenantStatus_TENANT_STATUS_DEPROVISIONED,
			expected: "deprovisioned",
		},
		{
			name:     "unspecified",
			input:    tenantv1.TenantStatus_TENANT_STATUS_UNSPECIFIED,
			expected: "unspecified",
		},
		{
			name:     "unknown value",
			input:    tenantv1.TenantStatus(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
