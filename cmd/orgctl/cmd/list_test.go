package cmd

import (
	"testing"

	organizationv1 "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	"github.com/stretchr/testify/assert"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected organizationv1.OrganizationStatus
		wantErr  bool
	}{
		{
			name:     "active lowercase",
			input:    "active",
			expected: organizationv1.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE,
			wantErr:  false,
		},
		{
			name:     "active uppercase",
			input:    "ACTIVE",
			expected: organizationv1.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE,
			wantErr:  false,
		},
		{
			name:     "active mixed case",
			input:    "Active",
			expected: organizationv1.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE,
			wantErr:  false,
		},
		{
			name:     "suspended",
			input:    "suspended",
			expected: organizationv1.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED,
			wantErr:  false,
		},
		{
			name:     "deprovisioned",
			input:    "deprovisioned",
			expected: organizationv1.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED,
			wantErr:  false,
		},
		{
			name:     "invalid status",
			input:    "invalid",
			expected: organizationv1.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED,
			wantErr:  true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: organizationv1.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED,
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
		input    organizationv1.OrganizationStatus
		expected string
	}{
		{
			name:     "active",
			input:    organizationv1.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE,
			expected: "active",
		},
		{
			name:     "suspended",
			input:    organizationv1.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED,
			expected: "suspended",
		},
		{
			name:     "deprovisioned",
			input:    organizationv1.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED,
			expected: "deprovisioned",
		},
		{
			name:     "unspecified",
			input:    organizationv1.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED,
			expected: "unspecified",
		},
		{
			name:     "unknown value",
			input:    organizationv1.OrganizationStatus(99),
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
