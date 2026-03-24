package tenant

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTenantID_valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"alphanumeric", "acme123"},
		{"underscores", "acme_corp"},
		{"single_char", "a"},
		{"max_length_50", "a2345678901234567890123456789012345678901234567890"},
		{"all_digits", "12345"},
		{"uppercase", "ACME"},
		{"mixed_case", "AcMe_123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tid, err := NewTenantID(tt.input)
			require.NoError(t, err)
			assert.Equal(t, TenantID(tt.input), tid)
		})
	}
}

func TestNewTenantID_invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"hyphen", "acme-corp"},
		{"space", "acme corp"},
		{"dot", "acme.corp"},
		{"special_chars", "acme@corp"},
		{"too_long_51", "a23456789012345678901234567890123456789012345678901"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTenantID(tt.input)
			assert.ErrorIs(t, err, ErrInvalidTenantID)
		})
	}
}

func TestMustNewTenantID_success(t *testing.T) {
	tid := MustNewTenantID("acme_corp")
	assert.Equal(t, TenantID("acme_corp"), tid)
}

func TestMustNewTenantID_panics(t *testing.T) {
	assert.Panics(t, func() {
		MustNewTenantID("")
	})
}

func TestTenantID_String(t *testing.T) {
	tid := TenantID("acme_corp")
	assert.Equal(t, "acme_corp", tid.String())
}

func TestTenantID_SchemaName(t *testing.T) {
	tests := []struct {
		name     string
		id       TenantID
		expected string
	}{
		{"lowercase", TenantID("acme"), "org_acme"},
		{"uppercase_normalized", TenantID("ACME"), "org_acme"},
		{"mixed_case", TenantID("AcMe_Corp"), "org_acme_corp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.id.SchemaName())
		})
	}
}

func TestTenantID_IsEmpty(t *testing.T) {
	assert.True(t, TenantID("").IsEmpty())
	assert.False(t, TenantID("acme").IsEmpty())
}
