package accounttype

import "errors"

// Domain error sentinels for AccountTypeDefinition operations.
var (
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
)
