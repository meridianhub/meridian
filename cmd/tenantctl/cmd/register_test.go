package cmd

import (
	"strings"
	"testing"
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
