package validation

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
