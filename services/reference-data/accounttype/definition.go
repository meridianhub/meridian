package accounttype

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Definition defines the structural and behavioral characteristics
// of an account type within the ledger system.
// External callers reference this as accounttype.Definition.
type Definition struct {
	// ID is the unique identifier for this definition.
	ID uuid.UUID

	// Code is the human-readable account type code (e.g., "CUSTOMER_CURRENT").
	Code string

	// Version allows multiple versions of the same account type code.
	Version int

	// DisplayName is a human-readable name for the account type.
	DisplayName string

	// Description provides additional context about this account type.
	Description string

	// NormalBalance defines the expected sign of the balance under normal conditions.
	NormalBalance NormalBalance

	// BehaviorClass categorizes the accounting and operational behavior.
	BehaviorClass BehaviorClass

	// InstrumentCode is the instrument code associated with this account type (e.g., "GBP").
	InstrumentCode string

	// DefaultSagaPrefix is the saga prefix used for operations on accounts of this type.
	DefaultSagaPrefix string

	// DefaultConversionMethodID is the UUID of the default valuation method for currency conversion.
	// Must be set together with DefaultConversionMethodVersion (pair constraint).
	DefaultConversionMethodID *uuid.UUID

	// DefaultConversionMethodVersion is the version of the default conversion method.
	// Must be set together with DefaultConversionMethodID (pair constraint).
	DefaultConversionMethodVersion *int

	// ValuationMethods lists the valuation method templates associated with this account type.
	ValuationMethods []ValuationMethodTemplate

	// ValidationCEL is a CEL expression for validating account operations.
	ValidationCEL string

	// BucketingCEL is a CEL expression for determining fungibility buckets.
	BucketingCEL string

	// EligibilityCEL is a CEL expression for determining account eligibility.
	EligibilityCEL string

	// AttributeSchema defines the JSON schema for allowed attributes (optional).
	AttributeSchema json.RawMessage

	// Attributes holds additional structured metadata for this account type.
	Attributes map[string]any

	// Status is the current lifecycle status.
	Status Status

	// IsSystem indicates this is a system account type seeded during tenant provisioning.
	IsSystem bool

	// SuccessorID is the UUID of the account type that replaces this one when deprecated.
	SuccessorID *uuid.UUID

	// CreatedAt is when this definition was created.
	CreatedAt time.Time

	// UpdatedAt is when this definition was last modified.
	UpdatedAt time.Time

	// ActivatedAt is when this definition transitioned to ACTIVE (nil if never activated).
	ActivatedAt *time.Time

	// DeprecatedAt is when this definition transitioned to DEPRECATED (nil if not deprecated).
	DeprecatedAt *time.Time
}

// ValuationMethodTemplate defines a valuation method associated with an account type.
type ValuationMethodTemplate struct {
	// ID is the unique identifier for this template.
	ID uuid.UUID

	// AccountTypeID is the UUID of the parent account type Definition.
	AccountTypeID uuid.UUID

	// InputInstrument is the instrument code that is the input for this valuation method.
	InputInstrument string

	// ValuationMethodID is the UUID of the referenced valuation method.
	ValuationMethodID uuid.UUID

	// ValuationMethodVersion is the version of the referenced valuation method.
	ValuationMethodVersion int

	// Parameters holds additional configuration for this valuation method template.
	Parameters map[string]any

	// Status is the current lifecycle status.
	Status Status

	// SuccessorID is the UUID of the template that replaces this one when deprecated.
	SuccessorID *uuid.UUID

	// CreatedAt is when this template was created.
	CreatedAt time.Time

	// UpdatedAt is when this template was last modified.
	UpdatedAt time.Time
}

// NewDefinitionParams holds the input parameters for creating a new account type Definition.
type NewDefinitionParams struct {
	Code                           string
	DisplayName                    string
	Description                    string
	NormalBalance                  string
	BehaviorClass                  string
	InstrumentCode                 string
	DefaultSagaPrefix              string
	DefaultConversionMethodID      *uuid.UUID
	DefaultConversionMethodVersion *int
	ValidationCEL                  string
	BucketingCEL                   string
	EligibilityCEL                 string
	AttributeSchema                json.RawMessage
	Attributes                     map[string]any
}

// NewDefinition creates a new account type Definition in DRAFT status.
// Code, BehaviorClass, NormalBalance, and InstrumentCode are normalized to uppercase.
// Returns an error if any typed enum field is invalid or the pair constraint is violated.
func NewDefinition(p NewDefinitionParams) (*Definition, error) {
	normalBalance := NormalBalance(strings.ToUpper(p.NormalBalance))
	behaviorClass := BehaviorClass(strings.ToUpper(p.BehaviorClass))
	code := strings.ToUpper(p.Code)
	instrumentCode := strings.ToUpper(p.InstrumentCode)

	if !normalBalance.IsValid() {
		return nil, ErrInvalidNormalBalance
	}
	if !behaviorClass.IsValid() {
		return nil, ErrInvalidBehaviorClass
	}

	// Pair constraint: both must be set or both must be nil.
	if (p.DefaultConversionMethodID == nil) != (p.DefaultConversionMethodVersion == nil) {
		return nil, ErrConversionMethodPair
	}

	now := time.Now().UTC()

	return &Definition{
		ID:                             uuid.New(),
		Code:                           code,
		Version:                        1,
		DisplayName:                    p.DisplayName,
		Description:                    p.Description,
		NormalBalance:                  normalBalance,
		BehaviorClass:                  behaviorClass,
		InstrumentCode:                 instrumentCode,
		DefaultSagaPrefix:              p.DefaultSagaPrefix,
		DefaultConversionMethodID:      p.DefaultConversionMethodID,
		DefaultConversionMethodVersion: p.DefaultConversionMethodVersion,
		ValuationMethods:               []ValuationMethodTemplate{},
		ValidationCEL:                  p.ValidationCEL,
		BucketingCEL:                   p.BucketingCEL,
		EligibilityCEL:                 p.EligibilityCEL,
		AttributeSchema:                p.AttributeSchema,
		Attributes:                     p.Attributes,
		Status:                         StatusDraft,
		IsSystem:                       false,
		CreatedAt:                      now,
		UpdatedAt:                      now,
	}, nil
}
