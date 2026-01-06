package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFieldValidator_Validate_AllFieldsPresent(t *testing.T) {
	validator := NewFieldValidator()

	row := &ImportRow{
		LineNumber:     1,
		AccountID:      "account-123",
		InstrumentCode: "USD",
		Amount:         "100.50",
		BucketKey:      "bucket-key-1",
	}

	errors := validator.Validate(row)
	assert.Empty(t, errors)
}

func TestFieldValidator_Validate_MissingAccountID(t *testing.T) {
	validator := NewFieldValidator()

	row := &ImportRow{
		LineNumber:     1,
		AccountID:      "", // Missing
		InstrumentCode: "USD",
		Amount:         "100.50",
		BucketKey:      "bucket-key-1",
	}

	errors := validator.Validate(row)
	assert.Len(t, errors, 1)
	assert.Equal(t, "account_id", errors[0].Field)
}

func TestFieldValidator_Validate_MissingMultipleFields(t *testing.T) {
	validator := NewFieldValidator()

	row := &ImportRow{
		LineNumber:     1,
		AccountID:      "",    // Missing
		InstrumentCode: "",    // Missing
		Amount:         "",    // Missing
		BucketKey:      "key", // Present
	}

	errors := validator.Validate(row)
	assert.Len(t, errors, 3)

	fieldNames := make(map[string]bool)
	for _, err := range errors {
		fieldNames[err.Field] = true
	}

	assert.True(t, fieldNames["account_id"])
	assert.True(t, fieldNames["instrument_code"])
	assert.True(t, fieldNames["amount"])
}

func TestFieldValidator_Validate_WhitespaceOnlyValue(t *testing.T) {
	validator := NewFieldValidator()

	row := &ImportRow{
		LineNumber:     1,
		AccountID:      "   ", // Whitespace only - should be treated as empty
		InstrumentCode: "USD",
		Amount:         "100.50",
		BucketKey:      "key",
	}

	errors := validator.Validate(row)
	assert.Len(t, errors, 1)
	assert.Equal(t, "account_id", errors[0].Field)
}

func TestFieldValidator_ValidateBatch(t *testing.T) {
	validator := NewFieldValidator()

	rows := []ImportRow{
		{LineNumber: 1, AccountID: "acc-1", InstrumentCode: "USD", Amount: "100", BucketKey: "key"},
		{LineNumber: 2, AccountID: "", InstrumentCode: "USD", Amount: "100", BucketKey: "key"}, // Error
		{LineNumber: 3, AccountID: "acc-3", InstrumentCode: "USD", Amount: "100", BucketKey: "key"},
		{LineNumber: 4, AccountID: "acc-4", InstrumentCode: "", Amount: "", BucketKey: "key"}, // Two errors
	}

	results := validator.ValidateBatch(rows)

	// Line 1 and 3 have no errors, so they shouldn't appear
	assert.NotContains(t, results, 1)
	assert.NotContains(t, results, 3)

	// Line 2 has one error
	assert.Contains(t, results, 2)
	assert.Len(t, results[2], 1)

	// Line 4 has two errors
	assert.Contains(t, results, 4)
	assert.Len(t, results[4], 2)
}

func TestFieldValidator_CustomFields(t *testing.T) {
	customFields := []RequiredField{
		{
			Name:      "custom_field",
			Extractor: func(r *ImportRow) string { return r.Attributes["custom_field"] },
		},
	}

	validator := NewFieldValidator(customFields...)

	// Row with custom field present
	row := &ImportRow{
		LineNumber: 1,
		Attributes: map[string]string{"custom_field": "value"},
	}
	errors := validator.Validate(row)
	assert.Empty(t, errors)

	// Row with custom field missing
	row2 := &ImportRow{
		LineNumber: 2,
		Attributes: map[string]string{},
	}
	errors = validator.Validate(row2)
	assert.Len(t, errors, 1)
	assert.Equal(t, "custom_field", errors[0].Field)
}

func TestValidateInstrumentCode(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{"valid uppercase", "USD", false},
		{"valid with numbers", "CO2E", false},
		{"valid with underscore", "CARBON_CREDIT", false},
		{"empty", "", true},
		{"lowercase", "usd", true},
		{"mixed case", "Usd", true},
		{"special chars", "US$", true},
		{"starts with number", "1USD", true},
		{"whitespace", "  ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstrumentCode(tt.code)
			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestValidateAmount(t *testing.T) {
	tests := []struct {
		name    string
		amount  string
		wantErr bool
	}{
		{"positive integer", "100", false},
		{"positive decimal", "100.50", false},
		{"negative integer", "-100", false},
		{"negative decimal", "-100.50", false},
		{"zero", "0", false},
		{"leading plus", "+100", false},
		{"empty", "", true},
		{"not a number", "abc", true},
		{"multiple decimals", "100.50.25", true},
		{"only sign", "-", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAmount(tt.amount)
			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestValidateBucketKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid key", "bucket-123", false},
		{"empty", "", true},
		{"whitespace", "   ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBucketKey(tt.key)
			if tt.wantErr {
				assert.NotNil(t, err)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestValidateBucketKey_MaxLength(t *testing.T) {
	// Create a string longer than 255 characters
	longKey := make([]byte, 300)
	for i := range longKey {
		longKey[i] = 'a'
	}

	err := ValidateBucketKey(string(longKey))
	assert.NotNil(t, err)
	assert.Contains(t, err.Reason, "exceeds maximum length")
}

func TestFieldError_Error(t *testing.T) {
	// With value
	err := &FieldError{
		Field:  "test_field",
		Value:  "bad_value",
		Reason: "invalid format",
	}
	assert.Contains(t, err.Error(), "test_field")
	assert.Contains(t, err.Error(), "bad_value")
	assert.Contains(t, err.Error(), "invalid format")

	// Without value
	err2 := &FieldError{
		Field:  "test_field",
		Reason: "is required",
	}
	assert.Contains(t, err2.Error(), "test_field")
	assert.Contains(t, err2.Error(), "is required")
}

func TestIsValidInstrumentCode(t *testing.T) {
	assert.True(t, isValidInstrumentCode("USD"))
	assert.True(t, isValidInstrumentCode("CARBON_CREDIT"))
	assert.True(t, isValidInstrumentCode("A1"))
	assert.False(t, isValidInstrumentCode(""))
	assert.False(t, isValidInstrumentCode("1USD"))
	assert.False(t, isValidInstrumentCode("usd"))
}

func TestIsValidDecimalString(t *testing.T) {
	assert.True(t, isValidDecimalString("100"))
	assert.True(t, isValidDecimalString("100.5"))
	assert.True(t, isValidDecimalString("-100"))
	assert.True(t, isValidDecimalString("+100.5"))
	assert.True(t, isValidDecimalString("0"))
	assert.True(t, isValidDecimalString(".5"))
	assert.False(t, isValidDecimalString(""))
	assert.False(t, isValidDecimalString("-"))
	assert.False(t, isValidDecimalString("abc"))
	assert.False(t, isValidDecimalString("1.2.3"))
}

func TestTruncateForDisplay(t *testing.T) {
	assert.Equal(t, "short", truncateForDisplay("short", 10))
	assert.Equal(t, "exactly10c", truncateForDisplay("exactly10c", 10))
	// "longerstring" is 12 chars, maxLen 11, so we get first 8 chars + "..." = "longerst..."
	assert.Equal(t, "longerst...", truncateForDisplay("longerstring", 11))
}
