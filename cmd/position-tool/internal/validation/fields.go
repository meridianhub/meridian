package validation

import (
	"fmt"
	"strings"
)

// RequiredField defines a field that must be present and non-empty.
type RequiredField struct {
	// Name is the field name for error messages.
	Name string

	// Extractor returns the field value from an ImportRow.
	Extractor func(*ImportRow) string
}

// DefaultRequiredFields returns the standard required fields for import rows.
func DefaultRequiredFields() []RequiredField {
	return []RequiredField{
		{
			Name:      "account_id",
			Extractor: func(r *ImportRow) string { return r.AccountID },
		},
		{
			Name:      "instrument_code",
			Extractor: func(r *ImportRow) string { return r.InstrumentCode },
		},
		{
			Name:      "amount",
			Extractor: func(r *ImportRow) string { return r.Amount },
		},
		{
			Name:      "bucket_key",
			Extractor: func(r *ImportRow) string { return r.BucketKey },
		},
	}
}

// FieldValidator validates that required fields are present and non-empty.
type FieldValidator struct {
	requiredFields []RequiredField
}

// NewFieldValidator creates a new field validator with the specified required fields.
// If no fields are provided, the default required fields are used.
func NewFieldValidator(fields ...RequiredField) *FieldValidator {
	if len(fields) == 0 {
		fields = DefaultRequiredFields()
	}
	return &FieldValidator{
		requiredFields: fields,
	}
}

// Validate checks that all required fields are present and non-empty.
// Returns all field errors, not just the first one found.
func (v *FieldValidator) Validate(row *ImportRow) []*FieldError {
	var errors []*FieldError

	for _, field := range v.requiredFields {
		value := field.Extractor(row)
		if strings.TrimSpace(value) == "" {
			errors = append(errors, &FieldError{
				Field:  field.Name,
				Value:  value,
				Reason: "required field is empty",
			})
		}
	}

	return errors
}

// ValidateBatch validates multiple rows and returns a map of line number to errors.
// Only rows with errors are included in the result.
func (v *FieldValidator) ValidateBatch(rows []ImportRow) map[int][]*FieldError {
	results := make(map[int][]*FieldError)

	for i := range rows {
		if errors := v.Validate(&rows[i]); len(errors) > 0 {
			results[rows[i].LineNumber] = errors
		}
	}

	return results
}

// ValidateAccountID performs specific validation for account IDs.
// Returns an error if the account ID is invalid.
func ValidateAccountID(accountID string) *FieldError {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return &FieldError{
			Field:  "account_id",
			Reason: "required field is empty",
		}
	}

	// Account IDs should be non-empty strings
	// Additional format validation can be added here if needed (e.g., UUID format)
	return nil
}

// ValidateInstrumentCode performs specific validation for instrument codes.
// Returns an error if the instrument code is invalid.
func ValidateInstrumentCode(code string) *FieldError {
	code = strings.TrimSpace(code)
	if code == "" {
		return &FieldError{
			Field:  "instrument_code",
			Reason: "required field is empty",
		}
	}

	// Instrument codes should be uppercase alphanumeric with underscores
	if !isValidInstrumentCode(code) {
		return &FieldError{
			Field:  "instrument_code",
			Value:  code,
			Reason: "must be uppercase alphanumeric with underscores (e.g., USD, CARBON_CREDIT)",
		}
	}

	return nil
}

// ValidateAmount performs specific validation for amounts.
// Returns an error if the amount is invalid.
func ValidateAmount(amount string) *FieldError {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return &FieldError{
			Field:  "amount",
			Reason: "required field is empty",
		}
	}

	// Basic numeric validation - more thorough validation happens during parsing
	if !isValidDecimalString(amount) {
		return &FieldError{
			Field:  "amount",
			Value:  amount,
			Reason: "must be a valid decimal number",
		}
	}

	return nil
}

// ValidateBucketKey performs specific validation for bucket keys.
// Returns an error if the bucket key is invalid.
func ValidateBucketKey(bucketKey string) *FieldError {
	bucketKey = strings.TrimSpace(bucketKey)
	if bucketKey == "" {
		return &FieldError{
			Field:  "bucket_key",
			Reason: "required field is empty",
		}
	}

	// Bucket keys have a max length
	const maxBucketKeyLength = 255
	if len(bucketKey) > maxBucketKeyLength {
		return &FieldError{
			Field:  "bucket_key",
			Value:  truncateForDisplay(bucketKey, 50),
			Reason: fmt.Sprintf("exceeds maximum length of %d characters", maxBucketKeyLength),
		}
	}

	return nil
}

// isValidInstrumentCode checks if a string is a valid instrument code.
// Valid codes: start with uppercase letter, contain only uppercase letters, digits, underscores.
func isValidInstrumentCode(code string) bool {
	if len(code) == 0 {
		return false
	}

	// Must start with uppercase letter
	if code[0] < 'A' || code[0] > 'Z' {
		return false
	}

	for _, c := range code {
		isUpper := c >= 'A' && c <= 'Z'
		isDigit := c >= '0' && c <= '9'
		isUnderscore := c == '_'
		if !isUpper && !isDigit && !isUnderscore {
			return false
		}
	}

	return true
}

// isValidDecimalString performs basic validation that a string could be a decimal.
func isValidDecimalString(s string) bool {
	if len(s) == 0 {
		return false
	}

	// Allow leading minus sign
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
		if len(s) == 1 {
			return false
		}
	}

	hasDigit := false
	hasDecimalPoint := false

	for i := start; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			hasDigit = true
		} else if c == '.' {
			if hasDecimalPoint {
				return false // Multiple decimal points
			}
			hasDecimalPoint = true
		} else {
			return false // Invalid character
		}
	}

	return hasDigit
}

// truncateForDisplay truncates a string for display in error messages.
func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
