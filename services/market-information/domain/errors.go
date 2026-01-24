// Package domain contains the domain models for the Market Information service.
package domain

import "errors"

// Domain errors for dataset definition operations.
// These follow the sentinel error pattern for consistent error handling.
var (
	// ErrDataSetNotFound indicates the requested dataset definition does not exist.
	ErrDataSetNotFound = errors.New("dataset definition not found")

	// ErrDataSetDeprecated indicates an operation was attempted on a deprecated dataset.
	ErrDataSetDeprecated = errors.New("dataset is deprecated")

	// ErrInvalidDataCategory indicates an unrecognized data category was provided.
	ErrInvalidDataCategory = errors.New("invalid data category")

	// ErrInvalidDataSetStatus indicates an unrecognized dataset status was provided.
	ErrInvalidDataSetStatus = errors.New("invalid dataset status")

	// ErrDuplicateDataSetCode indicates a dataset with the given code already exists.
	ErrDuplicateDataSetCode = errors.New("dataset code already exists")

	// ErrVersionMismatch indicates an optimistic locking conflict occurred.
	// The dataset was modified by another process since it was read.
	ErrVersionMismatch = errors.New("version mismatch: dataset was modified")

	// Validation errors for required fields.

	// ErrCodeRequired indicates the dataset code was not provided.
	ErrCodeRequired = errors.New("dataset code is required")

	// ErrNameRequired indicates the dataset name was not provided.
	ErrNameRequired = errors.New("dataset name is required")

	// ErrValidationExpressionRequired indicates the validation CEL expression was not provided.
	ErrValidationExpressionRequired = errors.New("validation expression is required")

	// ErrResolutionKeyExpressionRequired indicates the resolution key CEL expression was not provided.
	ErrResolutionKeyExpressionRequired = errors.New("resolution key expression is required")

	// MarketPriceObservation errors.

	// ErrObservationNotFound indicates the requested observation does not exist.
	ErrObservationNotFound = errors.New("observation not found")

	// ErrInvalidObservationValue indicates the observation value is invalid (e.g., negative).
	ErrInvalidObservationValue = errors.New("invalid observation value")

	// ErrInvalidTemporalBounds indicates the temporal bounds are invalid (ValidFrom >= ValidTo).
	ErrInvalidTemporalBounds = errors.New("invalid temporal bounds: valid from must be before valid to")

	// ErrObservationAlreadySuperseded indicates the observation has already been superseded.
	ErrObservationAlreadySuperseded = errors.New("observation already superseded")

	// ErrInvalidSupersedeTarget indicates the supersede target is invalid (nil UUID or self-reference).
	ErrInvalidSupersedeTarget = errors.New("invalid supersede target: cannot be nil or self-reference")

	// ErrDataSetCodeRequired indicates the dataset code was not provided for an observation.
	ErrDataSetCodeRequired = errors.New("dataset code is required")

	// ErrSourceIDRequired indicates the source ID was not provided for an observation.
	ErrSourceIDRequired = errors.New("source ID is required")

	// ErrResolutionKeyRequired indicates the resolution key was not provided for an observation.
	ErrResolutionKeyRequired = errors.New("resolution key is required")

	// ErrUnitRequired indicates the unit was not provided for an observation.
	ErrUnitRequired = errors.New("unit is required")

	// ErrInvalidQualityLevel indicates an invalid quality level was provided.
	ErrInvalidQualityLevel = errors.New("invalid quality level")

	// ErrCausationIDRequired indicates the causation ID was not provided for an observation.
	ErrCausationIDRequired = errors.New("causation ID is required")

	// ErrAccessDenied indicates the tenant lacks permission to access the dataset.
	// This occurs when attempting to access a RESTRICTED shared dataset without valid entitlements.
	ErrAccessDenied = errors.New("access denied: tenant not entitled to dataset")

	// DataSource errors for repository operations.

	// ErrDataSourceNotFound indicates the requested data source does not exist.
	ErrDataSourceNotFound = errors.New("data source not found")

	// ErrDuplicateDataSourceCode indicates a data source with the given code already exists.
	ErrDuplicateDataSourceCode = errors.New("data source code already exists")

	// Pagination errors.

	// ErrInvalidPageToken indicates the pagination token has an invalid format.
	ErrInvalidPageToken = errors.New("invalid page token format")
)
