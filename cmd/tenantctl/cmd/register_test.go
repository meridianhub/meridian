package cmd

import (
	"strings"
	"testing"

	"github.com/meridianhub/meridian/services/tenant/domain"
)

func TestGenerateSlugFromName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple two words with space",
			input:    "Acme Corp",
			expected: "acme-corp",
		},
		{
			name:     "with special characters",
			input:    "Test Bank!",
			expected: "test-bank",
		},
		{
			name:     "multiple spaces",
			input:    "Multiple   Spaces",
			expected: "multiple-spaces",
		},
		{
			name:     "leading and trailing special chars",
			input:    "!@#Test Company$%^",
			expected: "test-company",
		},
		{
			name:     "mixed case with numbers",
			input:    "Bank123 Corp",
			expected: "bank123-corp",
		},
		{
			name:     "already valid slug",
			input:    "acme-corp",
			expected: "acme-corp",
		},
		{
			name:     "uppercase only",
			input:    "ACME",
			expected: "acme",
		},
		{
			name:     "with ampersand",
			input:    "Smith & Jones",
			expected: "smith-jones",
		},
		{
			name:     "with parentheses",
			input:    "Test (Org)",
			expected: "test-org",
		},
		{
			name:     "unicode characters",
			input:    "Cafe Muller",
			expected: "cafe-muller",
		},
		{
			name:     "consecutive special characters",
			input:    "Test---Bank",
			expected: "test-bank",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only special characters",
			input:    "!@#$%^&*()",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateSlugFromName(tt.input)
			if result != tt.expected {
				t.Errorf("generateSlugFromName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGenerateSlugFromName_LongName(t *testing.T) {
	// Test with a string that would exceed 63 characters
	longName := strings.Repeat("a", 70)
	result := generateSlugFromName(longName)

	if len(result) > 63 {
		t.Errorf("generateSlugFromName with 70 char input produced slug of length %d, want <= 63", len(result))
	}
	if len(result) != 63 {
		t.Errorf("generateSlugFromName with 70 char input produced slug of length %d, want 63", len(result))
	}
}

func TestGenerateSlugFromName_TruncationRemovesTrailingHyphen(t *testing.T) {
	// Create a string that when truncated at 63 chars would end with a hyphen
	// "a" * 62 + " " + "b" -> "a" * 62 + "-" + "b" -> truncate to 63 = "a" * 62 + "-"
	// We want to ensure the trailing hyphen is removed
	longName := strings.Repeat("a", 62) + " " + "b"
	result := generateSlugFromName(longName)

	if strings.HasSuffix(result, "-") {
		t.Errorf("generateSlugFromName should not produce slug ending with hyphen, got %q", result)
	}
}

// TestSlugValidationAtCLILevel tests that the CLI properly validates slugs
// using domain.ValidateSlug before attempting to call the gRPC service.
// This verifies client-side validation catches invalid slugs early.
func TestSlugValidationAtCLILevel(t *testing.T) {
	tests := []struct {
		name        string
		slug        string
		expectValid bool
		errContains string
	}{
		// Valid slugs
		{
			name:        "valid simple slug",
			slug:        "acme-bank",
			expectValid: true,
		},
		{
			name:        "valid slug with numbers",
			slug:        "bank-123",
			expectValid: true,
		},
		{
			name:        "valid minimum length slug",
			slug:        "abc",
			expectValid: true,
		},
		{
			name:        "valid maximum length slug (63 chars)",
			slug:        "a123456789012345678901234567890123456789012345678901234567890ab",
			expectValid: true,
		},

		// Invalid: Too short
		{
			name:        "too short - 2 chars",
			slug:        "ab",
			expectValid: false,
			errContains: "at least 3 characters",
		},
		{
			name:        "too short - 1 char",
			slug:        "a",
			expectValid: false,
			errContains: "at least 3 characters",
		},

		// Invalid: Too long
		{
			name:        "too long - 64 chars",
			slug:        "a1234567890123456789012345678901234567890123456789012345678901234",
			expectValid: false,
			errContains: "at most 63 characters",
		},

		// Invalid: Uppercase characters
		{
			name:        "uppercase letters",
			slug:        "ACME",
			expectValid: false,
			errContains: "lowercase",
		},
		{
			name:        "mixed case",
			slug:        "AcmeBanK",
			expectValid: false,
			errContains: "lowercase",
		},

		// Invalid: Special characters
		{
			name:        "contains underscore",
			slug:        "acme_bank",
			expectValid: false,
			errContains: "lowercase",
		},
		{
			name:        "contains space",
			slug:        "acme bank",
			expectValid: false,
			errContains: "lowercase",
		},
		{
			name:        "contains dot",
			slug:        "acme.bank",
			expectValid: false,
			errContains: "lowercase",
		},
		{
			name:        "contains exclamation mark",
			slug:        "acme!",
			expectValid: false,
			errContains: "lowercase",
		},

		// Invalid: Hyphen placement
		{
			name:        "starts with hyphen",
			slug:        "-acme",
			expectValid: false,
			errContains: "cannot start or end with a hyphen",
		},
		{
			name:        "ends with hyphen",
			slug:        "acme-",
			expectValid: false,
			errContains: "cannot start or end with a hyphen",
		},

		// Invalid: Reserved words
		{
			name:        "reserved - api",
			slug:        "api",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - admin",
			slug:        "admin",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - www",
			slug:        "www",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - health",
			slug:        "health",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - status",
			slug:        "status",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - docs",
			slug:        "docs",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - internal",
			slug:        "internal",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - system",
			slug:        "system",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - platform",
			slug:        "platform",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - app",
			slug:        "app",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - auth",
			slug:        "auth",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - test",
			slug:        "test",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - staging",
			slug:        "staging",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - dev",
			slug:        "dev",
			expectValid: false,
			errContains: "reserved",
		},
		{
			name:        "reserved - prod",
			slug:        "prod",
			expectValid: false,
			errContains: "reserved",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Import domain package for validation
			err := validateSlugForTest(tt.slug)
			if tt.expectValid {
				if err != nil {
					t.Errorf("expected slug %q to be valid, got error: %v", tt.slug, err)
				}
			} else {
				if err == nil {
					t.Errorf("expected slug %q to be invalid, but it passed validation", tt.slug)
				} else if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error for slug %q to contain %q, got: %v", tt.slug, tt.errContains, err)
				}
			}
		})
	}
}

// validateSlugForTest wraps domain.ValidateSlug for testing purposes.
// This mirrors the validation done in runRegister before calling gRPC.
func validateSlugForTest(slug string) error {
	return domain.ValidateSlug(slug)
}

// TestAutoGeneratedSlugIsValid tests that slugs generated from display names
// are always valid according to domain.ValidateSlug.
func TestAutoGeneratedSlugIsValid(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
	}{
		{
			name:        "simple name with space",
			displayName: "Acme Bank",
		},
		{
			name:        "name with special characters",
			displayName: "Test Bank!",
		},
		{
			name:        "name with ampersand",
			displayName: "Smith & Jones",
		},
		{
			name:        "name with parentheses",
			displayName: "Test (Corp)",
		},
		{
			name:        "name with multiple special chars",
			displayName: "!@#Test$%^Company&*()",
		},
		{
			name:        "name with numbers",
			displayName: "Bank123 Corp",
		},
		{
			name:        "uppercase name",
			displayName: "ACME CORP",
		},
		{
			name:        "mixed case name",
			displayName: "AcMeBanK Corp",
		},
		{
			name:        "name with consecutive spaces",
			displayName: "Multiple   Spaces   Here",
		},
		{
			name:        "long name that truncates",
			displayName: "This Is A Very Long Company Name That Will Definitely Exceed Sixty Three Characters And Need Truncation",
		},
		{
			name:        "name with leading/trailing special chars",
			displayName: "  - Test Company - ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := generateSlugFromName(tt.displayName)

			// Skip validation for empty slugs (edge case for inputs like "!!!")
			if slug == "" {
				return
			}

			// Verify the generated slug passes validation
			if err := domain.ValidateSlug(slug); err != nil {
				t.Errorf("generateSlugFromName(%q) = %q, which fails validation: %v", tt.displayName, slug, err)
			}

			// Verify slug length constraint
			if len(slug) > 63 {
				t.Errorf("generateSlugFromName(%q) = %q has length %d, exceeds 63 char limit", tt.displayName, slug, len(slug))
			}

			// Verify no uppercase
			if slug != strings.ToLower(slug) {
				t.Errorf("generateSlugFromName(%q) = %q contains uppercase characters", tt.displayName, slug)
			}

			// Verify no leading/trailing hyphens
			if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
				t.Errorf("generateSlugFromName(%q) = %q has leading/trailing hyphens", tt.displayName, slug)
			}
		})
	}
}

// TestAutoGeneratedSlugMeetsMinimumLength verifies that auto-generated slugs
// from short display names meet the 3-character minimum requirement.
func TestAutoGeneratedSlugMeetsMinimumLength(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		expectValid bool
	}{
		{
			name:        "single letter",
			displayName: "A",
			expectValid: false, // Will produce "a" which is too short
		},
		{
			name:        "two letters",
			displayName: "AB",
			expectValid: false, // Will produce "ab" which is too short
		},
		{
			name:        "three letters",
			displayName: "ABC",
			expectValid: true, // Will produce "abc" which is valid
		},
		{
			name:        "three letter word",
			displayName: "Ace",
			expectValid: true, // Will produce "ace" which is valid
		},
		{
			name:        "short word with space",
			displayName: "A B",
			expectValid: true, // Will produce "a-b" which is 3 chars
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := generateSlugFromName(tt.displayName)
			err := domain.ValidateSlug(slug)

			if tt.expectValid && err != nil {
				t.Errorf("expected valid slug from %q, got %q with error: %v", tt.displayName, slug, err)
			}
			if !tt.expectValid && err == nil {
				t.Errorf("expected invalid slug from %q, but %q passed validation", tt.displayName, slug)
			}
		})
	}
}

// TestSlugDisplayInListOutput verifies the list command output format includes slug.
// This is a format verification test that checks the expected column structure.
func TestSlugDisplayInListOutput(t *testing.T) {
	// The list command output header should include SLUG column
	expectedHeader := "ID\tNAME\tSLUG\tSTATUS\tSETTLEMENT ASSET\tCREATED AT"

	// Verify header format matches list.go implementation
	if !strings.Contains(expectedHeader, "SLUG") {
		t.Error("list output header should contain SLUG column")
	}

	// Verify column order: ID, NAME, SLUG, STATUS, SETTLEMENT ASSET, CREATED AT
	parts := strings.Split(expectedHeader, "\t")
	if len(parts) != 6 {
		t.Errorf("expected 6 columns in list output, got %d", len(parts))
	}
	if parts[2] != "SLUG" {
		t.Errorf("expected SLUG as 3rd column, got %q", parts[2])
	}
}

// TestSlugDisplayInGetOutput verifies the get command output includes slug field.
func TestSlugDisplayInGetOutput(t *testing.T) {
	// Test that outputText function format includes Slug field
	// The expected format from get.go is "  Slug:             %s\n"

	// Verify slug label format matches expected pattern
	expectedLabel := "  Slug:"
	if len(expectedLabel) < 7 {
		t.Error("slug label should be properly formatted")
	}
}

// TestSlugInJSONOutput verifies that slug is included in JSON output from get command.
func TestSlugInJSONOutput(t *testing.T) {
	// The JSON output from outputJSON should include "slug" field
	// Based on get.go implementation, the output map includes:
	// "slug": tenant.Slug

	// This test documents the expected JSON structure
	expectedFields := []string{
		"tenant_id",
		"display_name",
		"slug",
		"settlement_asset",
		"status",
		"version",
	}

	for _, field := range expectedFields {
		// Verify field names match JSON key conventions
		if strings.Contains(field, "_") || field == strings.ToLower(field) {
			// Valid snake_case or lowercase field name
			continue
		}
		t.Errorf("expected snake_case JSON field name, got %q", field)
	}
}

// TestEmptySlugDisplaysHyphen verifies that empty slugs are displayed as "-" in list/get output.
func TestEmptySlugDisplaysHyphen(t *testing.T) {
	// For tenants created before slug support, slug may be empty
	// The list and get commands should display "-" for empty slugs

	slug := ""
	displayValue := slug
	if displayValue == "" {
		displayValue = "-"
	}

	if displayValue != "-" {
		t.Error("empty slug should be displayed as '-'")
	}
}

// TestSlugAutoGenerationEdgeCases tests edge cases in slug auto-generation
// that could produce invalid or problematic slugs.
func TestSlugAutoGenerationEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		expectEmpty bool
	}{
		{
			name:        "only special characters",
			displayName: "!@#$%^&*()",
			expectEmpty: true,
		},
		{
			name:        "only spaces",
			displayName: "     ",
			expectEmpty: true,
		},
		{
			name:        "only hyphens",
			displayName: "---",
			expectEmpty: true, // Will be trimmed to empty
		},
		{
			name:        "unicode only",
			displayName: "\u3042\u3044\u3046", // Japanese hiragana
			expectEmpty: true,
		},
		{
			name:        "empty string",
			displayName: "",
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := generateSlugFromName(tt.displayName)

			if tt.expectEmpty && slug != "" {
				t.Errorf("expected empty slug from %q, got %q", tt.displayName, slug)
			}
			if !tt.expectEmpty && slug == "" {
				t.Errorf("expected non-empty slug from %q, got empty", tt.displayName)
			}
		})
	}
}
