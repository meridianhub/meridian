package tenancy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/internal/platform/tenancy"
)

func TestNewTenantID_Valid(t *testing.T) {
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
		{"complex", "Tenant_123_ABC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tid, err := tenancy.NewTenantID(tt.input)
			if err != nil {
				t.Errorf("NewTenantID(%q) returned unexpected error: %v", tt.input, err)
			}
			if tid.String() != tt.input {
				t.Errorf("NewTenantID(%q).String() = %q, want %q", tt.input, tid.String(), tt.input)
			}
		})
	}
}

func TestNewTenantID_Invalid(t *testing.T) {
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
			tid, err := tenancy.NewTenantID(tt.input)
			if err == nil {
				t.Errorf("NewTenantID(%q) expected error, got nil", tt.input)
			}
			if !errors.Is(err, tenancy.ErrInvalidTenantID) {
				t.Errorf("NewTenantID(%q) error = %v, want ErrInvalidTenantID", tt.input, err)
			}
			if !tid.IsEmpty() {
				t.Errorf("NewTenantID(%q) returned non-empty TenantID on error", tt.input)
			}
		})
	}
}

func TestMustNewTenantID_Valid(t *testing.T) {
	tid := tenancy.MustNewTenantID("valid_tenant")
	if tid.String() != "valid_tenant" {
		t.Errorf("MustNewTenantID(\"valid_tenant\").String() = %q, want \"valid_tenant\"", tid.String())
	}
}

func TestMustNewTenantID_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustNewTenantID with invalid input did not panic")
		}
	}()

	tenancy.MustNewTenantID("invalid-tenant")
}

func TestTenantID_SchemaName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"acme_bank", "tenant_acme_bank"},
		{"bank123", "tenant_bank123"},
		{"ABC", "tenant_abc"}, // normalized to lowercase for PostgreSQL
		{"AcmeBank", "tenant_acmebank"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tid := tenancy.MustNewTenantID(tt.input)
			if got := tid.SchemaName(); got != tt.expected {
				t.Errorf("TenantID(%q).SchemaName() = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTenantID_IsEmpty(t *testing.T) {
	var emptyTID tenancy.TenantID
	if !emptyTID.IsEmpty() {
		t.Error("zero-value TenantID.IsEmpty() = false, want true")
	}

	validTID := tenancy.MustNewTenantID("tenant")
	if validTID.IsEmpty() {
		t.Error("valid TenantID.IsEmpty() = true, want false")
	}
}

func TestContextRoundTrip(t *testing.T) {
	original := tenancy.MustNewTenantID("test_tenant")
	ctx := tenancy.WithTenant(context.Background(), original)

	retrieved, ok := tenancy.FromContext(ctx)
	if !ok {
		t.Error("FromContext returned ok=false for context with tenant")
	}
	if retrieved != original {
		t.Errorf("FromContext returned %q, want %q", retrieved, original)
	}
}

func TestFromContext_Missing(t *testing.T) {
	ctx := context.Background()

	tid, ok := tenancy.FromContext(ctx)
	if ok {
		t.Error("FromContext returned ok=true for context without tenant")
	}
	if !tid.IsEmpty() {
		t.Errorf("FromContext returned non-empty TenantID %q for context without tenant", tid)
	}
}

func TestFromContext_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	tid, ok := tenancy.FromContext(nil)
	if ok {
		t.Error("FromContext returned ok=true for nil context")
	}
	if !tid.IsEmpty() {
		t.Errorf("FromContext returned non-empty TenantID %q for nil context", tid)
	}
}

func TestMustFromContext_Success(t *testing.T) {
	expected := tenancy.MustNewTenantID("test_tenant")
	ctx := tenancy.WithTenant(context.Background(), expected)

	got := tenancy.MustFromContext(ctx)
	if got != expected {
		t.Errorf("MustFromContext returned %q, want %q", got, expected)
	}
}

func TestMustFromContext_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustFromContext did not panic for context without tenant")
		}
	}()

	tenancy.MustFromContext(context.Background())
}

func TestContextOverwrite(t *testing.T) {
	first := tenancy.MustNewTenantID("first_tenant")
	second := tenancy.MustNewTenantID("second_tenant")

	ctx := tenancy.WithTenant(context.Background(), first)
	ctx = tenancy.WithTenant(ctx, second)

	got, ok := tenancy.FromContext(ctx)
	if !ok {
		t.Error("FromContext returned ok=false")
	}
	if got != second {
		t.Errorf("FromContext returned %q, want %q (the overwritten value)", got, second)
	}
}

func TestContextIsolation(t *testing.T) {
	tenant := tenancy.MustNewTenantID("isolated_tenant")
	parentCtx := context.Background()
	childCtx := tenancy.WithTenant(parentCtx, tenant)

	// Parent should not have tenant
	_, ok := tenancy.FromContext(parentCtx)
	if ok {
		t.Error("Parent context should not have tenant after child context is created")
	}

	// Child should have tenant
	got, ok := tenancy.FromContext(childCtx)
	if !ok {
		t.Error("Child context should have tenant")
	}
	if got != tenant {
		t.Errorf("Child context has wrong tenant: got %q, want %q", got, tenant)
	}
}
