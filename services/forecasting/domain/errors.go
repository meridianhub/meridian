// Package domain contains the domain models for the Forecasting service.
package domain

import "errors"

// Domain errors for forecasting strategy operations.
var (
	// ErrStrategyNotFound indicates the requested forecasting strategy does not exist.
	ErrStrategyNotFound = errors.New("forecasting strategy not found")

	// ErrStrategyDeprecated indicates an operation was attempted on a deprecated strategy.
	ErrStrategyDeprecated = errors.New("strategy is deprecated")

	// ErrDuplicateActiveStrategy indicates an active strategy with the same name already exists for the tenant.
	ErrDuplicateActiveStrategy = errors.New("active strategy with this name already exists for tenant")

	// ErrVersionMismatch indicates an optimistic locking conflict occurred.
	ErrVersionMismatch = errors.New("version mismatch: strategy was modified")

	// Validation errors for required fields.

	// ErrNameRequired indicates the strategy name was not provided.
	ErrNameRequired = errors.New("strategy name is required")

	// ErrTenantIDRequired indicates the tenant ID was not provided.
	ErrTenantIDRequired = errors.New("tenant ID is required")

	// ErrStarlarkCodeRequired indicates the Starlark script code was not provided.
	ErrStarlarkCodeRequired = errors.New("starlark code is required")

	// ErrScheduleRequired indicates the cron schedule was not provided.
	ErrScheduleRequired = errors.New("schedule is required")

	// ErrOutputDatasetCodeRequired indicates the output dataset code was not provided.
	ErrOutputDatasetCodeRequired = errors.New("output dataset code is required")

	// ErrInputDatasetCodesRequired indicates no input dataset codes were provided.
	ErrInputDatasetCodesRequired = errors.New("at least one input dataset code is required")

	// ErrInvalidHorizonHours indicates the horizon hours value is invalid.
	ErrInvalidHorizonHours = errors.New("horizon hours must be between 1 and 168")

	// ErrInvalidGranularityHours indicates the granularity hours value is invalid.
	ErrInvalidGranularityHours = errors.New("granularity hours must be between 1 and horizon hours")

	// ErrInvalidSchedule indicates the cron schedule expression is invalid.
	ErrInvalidSchedule = errors.New("invalid cron schedule expression")

	// Pagination errors.

	// ErrInvalidPageToken indicates the pagination token has an invalid format.
	ErrInvalidPageToken = errors.New("invalid page token format")
)
