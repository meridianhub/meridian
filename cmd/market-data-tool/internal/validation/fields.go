package validation

import (
	"fmt"
	"strings"

	"github.com/meridianhub/meridian/services/market-information/domain"
)

// ParseQualityString maps a CSV quality-level string to the two-axis quality
// model (ADR-0017): a confidence grade on Axis A (the returned QualityLevel) and
// a correction counter on Axis B (the returned revision int). The input is
// trimmed and matched case-insensitively.
//
// The four canonical confidence grades all denote original observations
// (revision 0):
//
//	ESTIMATE    -> (QualityLevelEstimate,    0)
//	PROVISIONAL -> (QualityLevelProvisional, 0)
//	ACTUAL      -> (QualityLevelActual,      0)
//	VERIFIED    -> (QualityLevelVerified,    0)
//
// REVISED is a legacy label retained for backward compatibility. It is not a
// confidence grade; it denotes a correction recorded at VERIFIED confidence, so
// it maps to the VERIFIED grade with revision 1:
//
//	REVISED     -> (QualityLevelVerified,    1)
//
// Any other value returns ErrUnknownQualityString.
func ParseQualityString(s string) (domain.QualityLevel, int, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ESTIMATE":
		return domain.QualityLevelEstimate, 0, nil
	case "PROVISIONAL":
		return domain.QualityLevelProvisional, 0, nil
	case "ACTUAL":
		return domain.QualityLevelActual, 0, nil
	case "VERIFIED":
		return domain.QualityLevelVerified, 0, nil
	case "REVISED":
		// Legacy label: a correction at VERIFIED confidence (Axis A) carrying a
		// non-zero revision (Axis B). REVISED is not a confidence grade of its own.
		return domain.QualityLevelVerified, 1, nil
	default:
		return 0, 0, fmt.Errorf("%w: %q", ErrUnknownQualityString, s)
	}
}

// DefaultRequiredFields lists the fields that must be present in every observation.
var DefaultRequiredFields = []string{
	"value",
	"quality_level",
	"observed_at",
}

// FieldValidator validates required fields are present.
type FieldValidator struct {
	requiredFields []string
}

// NewFieldValidator creates a new field validator with default required fields.
func NewFieldValidator() *FieldValidator {
	return &FieldValidator{
		requiredFields: DefaultRequiredFields,
	}
}

// NewFieldValidatorWithFields creates a field validator with custom required fields.
func NewFieldValidatorWithFields(fields []string) *FieldValidator {
	return &FieldValidator{
		requiredFields: fields,
	}
}

// Validate checks that all required fields are present in the observation row.
func (v *FieldValidator) Validate(row *ObservationRow) []*FieldError {
	if row == nil {
		return []*FieldError{{
			Field:  "row",
			Reason: "observation row is nil",
		}}
	}

	var errors []*FieldError

	if row.Value == "" {
		errors = append(errors, &FieldError{
			Field:  "value",
			Reason: "required field is empty",
		})
	}

	if row.QualityLevel == "" {
		errors = append(errors, &FieldError{
			Field:  "quality_level",
			Reason: "required field is empty",
		})
	}

	if row.ObservedAt.IsZero() {
		errors = append(errors, &FieldError{
			Field:  "observed_at",
			Reason: "required field is empty or invalid",
		})
	}

	return errors
}

// RequiredFields returns the list of required field names.
func (v *FieldValidator) RequiredFields() []string {
	return v.requiredFields
}
