package organization_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/organization"
)

func TestNewOrganizationID_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"lowercase", "acme"},
		{"uppercase", "ACME"},
		{"mixed_case", "AcmeBank"},
		{"with_numbers", "bank123"},
		{"with_underscore", "acme_bank"},
		{"single_char", "a"},
		{"max_length", strings.Repeat("a", 50)},
		{"all_numbers", "12345"},
		{"complex", "Org_123_ABC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgID, err := organization.NewOrganizationID(tt.input)
			if err != nil {
				t.Errorf("NewOrganizationID(%q) returned unexpected error: %v", tt.input, err)
			}
			if orgID.String() != tt.input {
				t.Errorf("NewOrganizationID(%q).String() = %q, want %q", tt.input, orgID.String(), tt.input)
			}
		})
	}
}

func TestNewOrganizationID_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"with_hyphen", "acme-bank"},
		{"with_space", "acme bank"},
		{"with_special_chars", "acme@bank"},
		{"with_dot", "acme.bank"},
		{"too_long", strings.Repeat("a", 51)},
		{"with_slash", "acme/bank"},
		{"unicode", "acme_bänk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgID, err := organization.NewOrganizationID(tt.input)
			if err == nil {
				t.Errorf("NewOrganizationID(%q) expected error, got nil", tt.input)
			}
			if !errors.Is(err, organization.ErrInvalidOrganizationID) {
				t.Errorf("NewOrganizationID(%q) error = %v, want ErrInvalidOrganizationID", tt.input, err)
			}
			if !orgID.IsEmpty() {
				t.Errorf("NewOrganizationID(%q) returned non-empty OrganizationID on error", tt.input)
			}
		})
	}
}

func TestMustNewOrganizationID_Valid(t *testing.T) {
	orgID := organization.MustNewOrganizationID("valid_org")
	if orgID.String() != "valid_org" {
		t.Errorf("MustNewOrganizationID(\"valid_org\").String() = %q, want \"valid_org\"", orgID.String())
	}
}

func TestMustNewOrganizationID_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustNewOrganizationID with invalid input did not panic")
		}
	}()

	organization.MustNewOrganizationID("invalid-org")
}

func TestOrganizationID_SchemaName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"acme_bank", "org_acme_bank"},
		{"bank123", "org_bank123"},
		{"ABC", "org_abc"}, // normalized to lowercase for PostgreSQL
		{"AcmeBank", "org_acmebank"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			orgID := organization.MustNewOrganizationID(tt.input)
			if got := orgID.SchemaName(); got != tt.expected {
				t.Errorf("OrganizationID(%q).SchemaName() = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestOrganizationID_IsEmpty(t *testing.T) {
	var emptyOrgID organization.OrganizationID
	if !emptyOrgID.IsEmpty() {
		t.Error("zero-value OrganizationID.IsEmpty() = false, want true")
	}

	validOrgID := organization.MustNewOrganizationID("org")
	if validOrgID.IsEmpty() {
		t.Error("valid OrganizationID.IsEmpty() = true, want false")
	}
}

func TestContextRoundTrip(t *testing.T) {
	original := organization.MustNewOrganizationID("test_org")
	ctx := organization.WithOrganization(context.Background(), original)

	retrieved, ok := organization.FromContext(ctx)
	if !ok {
		t.Error("FromContext returned ok=false for context with organization")
	}
	if retrieved != original {
		t.Errorf("FromContext returned %q, want %q", retrieved, original)
	}
}

func TestFromContext_Missing(t *testing.T) {
	ctx := context.Background()

	orgID, ok := organization.FromContext(ctx)
	if ok {
		t.Error("FromContext returned ok=true for context without organization")
	}
	if !orgID.IsEmpty() {
		t.Errorf("FromContext returned non-empty OrganizationID %q for context without organization", orgID)
	}
}

func TestFromContext_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	orgID, ok := organization.FromContext(nil)
	if ok {
		t.Error("FromContext returned ok=true for nil context")
	}
	if !orgID.IsEmpty() {
		t.Errorf("FromContext returned non-empty OrganizationID %q for nil context", orgID)
	}
}

func TestMustFromContext_Success(t *testing.T) {
	expected := organization.MustNewOrganizationID("test_org")
	ctx := organization.WithOrganization(context.Background(), expected)

	got := organization.MustFromContext(ctx)
	if got != expected {
		t.Errorf("MustFromContext returned %q, want %q", got, expected)
	}
}

func TestMustFromContext_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustFromContext did not panic for context without organization")
		}
	}()

	organization.MustFromContext(context.Background())
}

func TestContextOverwrite(t *testing.T) {
	first := organization.MustNewOrganizationID("first_org")
	second := organization.MustNewOrganizationID("second_org")

	ctx := organization.WithOrganization(context.Background(), first)
	ctx = organization.WithOrganization(ctx, second)

	got, ok := organization.FromContext(ctx)
	if !ok {
		t.Error("FromContext returned ok=false")
	}
	if got != second {
		t.Errorf("FromContext returned %q, want %q (the overwritten value)", got, second)
	}
}

func TestContextIsolation(t *testing.T) {
	org := organization.MustNewOrganizationID("isolated_org")
	parentCtx := context.Background()
	childCtx := organization.WithOrganization(parentCtx, org)

	// Parent should not have organization
	_, ok := organization.FromContext(parentCtx)
	if ok {
		t.Error("Parent context should not have organization after child context is created")
	}

	// Child should have organization
	got, ok := organization.FromContext(childCtx)
	if !ok {
		t.Error("Child context should have organization")
	}
	if got != org {
		t.Errorf("Child context has wrong organization: got %q, want %q", got, org)
	}
}

func TestRequireFromContext_Success(t *testing.T) {
	expected := organization.MustNewOrganizationID("test_org")
	ctx := organization.WithOrganization(context.Background(), expected)

	got, err := organization.RequireFromContext(ctx)
	if err != nil {
		t.Errorf("RequireFromContext returned unexpected error: %v", err)
	}
	if got != expected {
		t.Errorf("RequireFromContext returned %q, want %q", got, expected)
	}
}

func TestRequireFromContext_Missing(t *testing.T) {
	ctx := context.Background()

	orgID, err := organization.RequireFromContext(ctx)
	if err == nil {
		t.Error("RequireFromContext expected error for context without organization")
	}
	if !errors.Is(err, organization.ErrMissingOrganizationContext) {
		t.Errorf("RequireFromContext error = %v, want ErrMissingOrganizationContext", err)
	}
	if !orgID.IsEmpty() {
		t.Errorf("RequireFromContext returned non-empty OrganizationID %q on error", orgID)
	}
}

func TestRequireFromContext_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	orgID, err := organization.RequireFromContext(nil)
	if err == nil {
		t.Error("RequireFromContext expected error for nil context")
	}
	if !errors.Is(err, organization.ErrMissingOrganizationContext) {
		t.Errorf("RequireFromContext error = %v, want ErrMissingOrganizationContext", err)
	}
	if !orgID.IsEmpty() {
		t.Errorf("RequireFromContext returned non-empty OrganizationID %q for nil context", orgID)
	}
}
