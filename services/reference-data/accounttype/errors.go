package accounttype

import "errors"

// Domain error sentinels for AccountTypeDefinition operations.
var (
	// ErrNotFound is returned when an account type definition cannot be found.
	ErrNotFound = errors.New("account type definition not found")

	// ErrInvalidCEL is returned when a CEL expression fails to compile.
	ErrInvalidCEL = errors.New("invalid CEL expression")

	// ErrNotDraft is returned when an operation requires DRAFT status but the definition is not in DRAFT.
	ErrNotDraft = errors.New("account type definition is not in DRAFT status")

	// ErrNotActive is returned when an operation requires ACTIVE status but the definition is not ACTIVE.
	ErrNotActive = errors.New("account type definition is not ACTIVE")

	// ErrFieldImmutable is returned when attempting to modify an immutable field after creation.
	ErrFieldImmutable = errors.New("field is immutable after creation")

	// ErrOptimisticLock is returned when concurrent modification is detected.
	ErrOptimisticLock = errors.New("concurrent modification detected")

	// ErrActiveCodeExists is returned when an ACTIVE definition already exists for the given code.
	ErrActiveCodeExists = errors.New("an ACTIVE definition already exists for this code")

	// ErrSagaNotFound is returned when no saga is found for the given prefix and operation.
	ErrSagaNotFound = errors.New("no saga found for the given prefix and operation")

	// ErrInvalidBehaviorClass is returned when the provided behavior class is not a recognized value.
	ErrInvalidBehaviorClass = errors.New("invalid behavior class")

	// ErrInvalidNormalBalance is returned when the provided normal balance is not a recognized value.
	ErrInvalidNormalBalance = errors.New("invalid normal balance")

	// ErrInvalidStatus is returned when the provided status is not a recognized value.
	ErrInvalidStatus = errors.New("invalid status")

	// ErrConversionMethodPair is returned when DefaultConversionMethodID and DefaultConversionMethodVersion
	// are not both set or both nil (pair constraint violation).
	ErrConversionMethodPair = errors.New("DefaultConversionMethodID and DefaultConversionMethodVersion must both be set or both be nil")

	// ErrSuccessorWriteOnce is returned when attempting to change a successor_id that is already set.
	ErrSuccessorWriteOnce = errors.New("cannot modify successor_id once set (write-once semantics)")

	// ErrInvalidInstrument is returned when the referenced instrument does not exist or is not ACTIVE.
	ErrInvalidInstrument = errors.New("instrument does not exist or is not ACTIVE")

	// ErrInvalidConversionMethod is returned when the referenced default conversion method does not exist.
	ErrInvalidConversionMethod = errors.New("default conversion method does not exist")

	// ErrInvalidValuationMethod is returned when a valuation method template references an invalid method.
	ErrInvalidValuationMethod = errors.New("valuation method template references an invalid method or instrument")

	// ErrInvalidAttributeSchema is returned when the attribute_schema is not valid JSON Schema.
	ErrInvalidAttributeSchema = errors.New("attribute_schema is not valid JSON Schema")

	// ErrAttributesInvalid is returned when attributes do not validate against the attribute_schema.
	ErrAttributesInvalid = errors.New("attributes do not validate against attribute_schema")
)
