// Package validation provides validation pipeline for market data observation imports.
package validation

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for validation operations.
var (
	// ErrDatasetNotFound indicates the dataset code does not exist.
	ErrDatasetNotFound = errors.New("dataset not found")

	// ErrDatasetNotActive indicates the dataset exists but is not in ACTIVE status.
	ErrDatasetNotActive = errors.New("dataset is not in ACTIVE status")

	// ErrInvalidAttributeSchema indicates attributes do not conform to the dataset's schema.
	ErrInvalidAttributeSchema = errors.New("attributes do not conform to dataset schema")

	// ErrCELValidationFailed indicates the CEL validation expression failed.
	ErrCELValidationFailed = errors.New("CEL validation failed")

	// ErrNilDatasetChecker indicates the dataset checker is required but nil.
	ErrNilDatasetChecker = errors.New("dataset checker cannot be nil")

	// ErrUnknownQualityString indicates a quality string that does not map to a
	// known confidence grade on the four-level ladder (ADR-0017).
	ErrUnknownQualityString = errors.New("unknown quality level string")
)

// FieldError represents a validation error for a specific field.
type FieldError struct {
	// Field is the name of the field that failed validation.
	Field string

	// Value is the actual value that was invalid.
	Value string

	// Reason describes why the validation failed.
	Reason string
}

// Error implements the error interface.
func (e *FieldError) Error() string {
	if e.Value == "" {
		return fmt.Sprintf("field %q: %s", e.Field, e.Reason)
	}
	return fmt.Sprintf("field %q: %s (value: %q)", e.Field, e.Reason, e.Value)
}

// RowValidationError contains all validation errors for a single row.
type RowValidationError struct {
	// LineNumber is the 1-indexed line number in the source CSV.
	LineNumber int

	// Errors contains all validation errors for this row.
	Errors []error
}

// Error implements the error interface.
func (e *RowValidationError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("line %d: no errors", e.LineNumber)
	}
	if len(e.Errors) == 1 {
		return fmt.Sprintf("line %d: %v", e.LineNumber, e.Errors[0])
	}

	msgs := make([]string, 0, len(e.Errors))
	for _, err := range e.Errors {
		msgs = append(msgs, err.Error())
	}
	return fmt.Sprintf("line %d: %d validation errors: [%s]", e.LineNumber, len(e.Errors), strings.Join(msgs, "; "))
}

// HasErrors returns true if this row has any validation errors.
func (e *RowValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// AddError appends a validation error to this row.
func (e *RowValidationError) AddError(err error) {
	if err != nil {
		e.Errors = append(e.Errors, err)
	}
}

// AddFieldError creates and appends a field-specific error.
func (e *RowValidationError) AddFieldError(field, value, reason string) {
	e.Errors = append(e.Errors, &FieldError{
		Field:  field,
		Value:  value,
		Reason: reason,
	})
}

// Unwrap returns the underlying errors for errors.Is/As support.
func (e *RowValidationError) Unwrap() []error {
	return e.Errors
}

// SchemaValidationError represents a JSON Schema validation failure.
type SchemaValidationError struct {
	// Path is the JSON pointer path to the invalid field (e.g., "/tenor").
	Path string

	// Message is the validation error message from the schema validator.
	Message string

	// SchemaPath is the path in the schema that caused the validation failure.
	SchemaPath string
}

// Error implements the error interface.
func (e *SchemaValidationError) Error() string {
	if e.SchemaPath != "" {
		return fmt.Sprintf("schema validation failed at %q: %s (schema: %s)", e.Path, e.Message, e.SchemaPath)
	}
	return fmt.Sprintf("schema validation failed at %q: %s", e.Path, e.Message)
}

// MultiSchemaError contains multiple schema validation errors.
type MultiSchemaError struct {
	// Errors contains all schema validation errors.
	Errors []*SchemaValidationError
}

// Error implements the error interface.
func (e *MultiSchemaError) Error() string {
	if len(e.Errors) == 0 {
		return "no schema validation errors"
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}

	msgs := make([]string, 0, len(e.Errors))
	for _, err := range e.Errors {
		msgs = append(msgs, err.Error())
	}
	return fmt.Sprintf("%d schema validation errors: [%s]", len(e.Errors), strings.Join(msgs, "; "))
}

// Summary contains statistics about the validation run.
type Summary struct {
	// TotalRows is the total number of rows processed.
	TotalRows int

	// ValidRows is the number of rows that passed all validation.
	ValidRows int

	// InvalidRows is the number of rows that failed validation.
	InvalidRows int

	// MissingFieldCount is the number of missing required field errors.
	MissingFieldCount int

	// DatasetErrorCount is the number of dataset validation errors.
	DatasetErrorCount int

	// SchemaErrorCount is the number of attribute schema validation errors.
	SchemaErrorCount int

	// CELWarningCount is the number of CEL preview warnings.
	CELWarningCount int
}
