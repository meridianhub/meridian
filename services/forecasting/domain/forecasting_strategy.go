// Package domain contains the domain models for the Forecasting service.
package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// ForecastingStrategy represents a forecasting strategy aggregate root.
// This is an immutable value type - all modification methods return a new instance.
//
// A forecasting strategy defines a Starlark script that generates forward curve
// predictions from market data inputs. Each strategy specifies:
//   - The Starlark source code to execute
//   - Input datasets to read from Market Data Service
//   - Output dataset for the forward curve
//   - Forecast horizon and granularity
//   - A cron schedule for periodic execution
//
// Lifecycle states:
//   - DRAFT: Initial state, strategy is being configured
//   - ACTIVE: Strategy is scheduled for execution
//   - DEPRECATED: Strategy is retired (terminal state)
type ForecastingStrategy struct {
	id                         uuid.UUID
	tenantID                   string
	name                       string
	description                string
	starlarkCode               string
	horizonHours               int
	granularityHours           int
	schedule                   string
	inputDatasetCodes          []string
	outputDatasetCode          string
	referenceDataResolutionKey string
	status                     StrategyStatus
	version                    int64
	createdAt                  time.Time
	updatedAt                  time.Time
}

// NewForecastingStrategy creates a new ForecastingStrategy with validated fields.
// Returns a value type (not pointer) following the immutability pattern.
//
// Initial state:
//   - Status: DRAFT
//   - Version: 1
//
// Validation:
//   - tenantID cannot be empty
//   - name cannot be empty
//   - starlarkCode cannot be empty
//   - horizonHours must be between 1 and 168
//   - granularityHours must be between 1 and horizonHours
//   - schedule must be a valid cron expression
//   - inputDatasetCodes must have at least one entry
//   - outputDatasetCode cannot be empty
func NewForecastingStrategy(
	tenantID string,
	name string,
	description string,
	starlarkCode string,
	horizonHours int,
	granularityHours int,
	schedule string,
	inputDatasetCodes []string,
	outputDatasetCode string,
	referenceDataResolutionKey string,
) (ForecastingStrategy, error) {
	if err := validateNewStrategy(tenantID, name, starlarkCode, horizonHours, granularityHours, schedule, inputDatasetCodes, outputDatasetCode); err != nil {
		return ForecastingStrategy{}, err
	}

	// Defensive copy of input slice
	codes := make([]string, len(inputDatasetCodes))
	copy(codes, inputDatasetCodes)

	now := time.Now()
	return ForecastingStrategy{
		id:                         uuid.New(),
		tenantID:                   tenantID,
		name:                       name,
		description:                description,
		starlarkCode:               starlarkCode,
		horizonHours:               horizonHours,
		granularityHours:           granularityHours,
		schedule:                   schedule,
		inputDatasetCodes:          codes,
		outputDatasetCode:          outputDatasetCode,
		referenceDataResolutionKey: referenceDataResolutionKey,
		status:                     StrategyStatusDraft,
		version:                    1,
		createdAt:                  now,
		updatedAt:                  now,
	}, nil
}

// validateNewStrategy checks all required fields and constraints for a new strategy.
func validateNewStrategy(
	tenantID, name, starlarkCode string,
	horizonHours, granularityHours int,
	schedule string,
	inputDatasetCodes []string,
	outputDatasetCode string,
) error {
	if tenantID == "" {
		return ErrTenantIDRequired
	}
	if name == "" {
		return ErrNameRequired
	}
	if starlarkCode == "" {
		return ErrStarlarkCodeRequired
	}
	if horizonHours < 1 || horizonHours > 168 {
		return ErrInvalidHorizonHours
	}
	if granularityHours < 1 || granularityHours > horizonHours {
		return ErrInvalidGranularityHours
	}
	if schedule == "" {
		return ErrScheduleRequired
	}
	if _, err := cron.ParseStandard(schedule); err != nil {
		return ErrInvalidSchedule
	}
	if len(inputDatasetCodes) == 0 {
		return ErrInputDatasetCodesRequired
	}
	if outputDatasetCode == "" {
		return ErrOutputDatasetCodeRequired
	}
	return nil
}

// Activate transitions the strategy to ACTIVE status.
// Returns a new instance with updated status.
// Returns error if the transition is not valid (only DRAFT -> ACTIVE is allowed).
func (s ForecastingStrategy) Activate() (ForecastingStrategy, error) {
	if err := ValidateStatusTransition(s.status, StrategyStatusActive); err != nil {
		return s, err
	}
	return s.withStatusChange(StrategyStatusActive), nil
}

// Deprecate transitions the strategy to DEPRECATED status.
// Returns a new instance with updated status.
// Valid transitions: DRAFT -> DEPRECATED, ACTIVE -> DEPRECATED
func (s ForecastingStrategy) Deprecate() (ForecastingStrategy, error) {
	if err := ValidateStatusTransition(s.status, StrategyStatusDeprecated); err != nil {
		return s, err
	}
	return s.withStatusChange(StrategyStatusDeprecated), nil
}

// UpdateStarlarkCode updates the Starlark script code.
// Returns a new instance with updated code and incremented version.
// Returns error if strategy is deprecated or code is empty.
func (s ForecastingStrategy) UpdateStarlarkCode(code string) (ForecastingStrategy, error) {
	if s.status == StrategyStatusDeprecated {
		return s, ErrStrategyDeprecated
	}
	if code == "" {
		return s, ErrStarlarkCodeRequired
	}

	updated := s.copyWithUpdatedTime()
	updated.starlarkCode = code
	updated.version++
	return updated, nil
}

// UpdateDescription updates the strategy description.
// Returns a new instance with updated description and incremented version.
// Returns error if strategy is deprecated.
func (s ForecastingStrategy) UpdateDescription(description string) (ForecastingStrategy, error) {
	if s.status == StrategyStatusDeprecated {
		return s, ErrStrategyDeprecated
	}

	updated := s.copyWithUpdatedTime()
	updated.description = description
	updated.version++
	return updated, nil
}

// UpdateSchedule updates the cron schedule expression.
// Returns a new instance with updated schedule and incremented version.
// Returns error if strategy is deprecated or schedule is invalid.
func (s ForecastingStrategy) UpdateSchedule(schedule string) (ForecastingStrategy, error) {
	if s.status == StrategyStatusDeprecated {
		return s, ErrStrategyDeprecated
	}
	if schedule == "" {
		return s, ErrScheduleRequired
	}
	if _, err := cron.ParseStandard(schedule); err != nil {
		return s, ErrInvalidSchedule
	}

	updated := s.copyWithUpdatedTime()
	updated.schedule = schedule
	updated.version++
	return updated, nil
}

// withStatusChange creates a new instance with updated status.
func (s ForecastingStrategy) withStatusChange(newStatus StrategyStatus) ForecastingStrategy {
	updated := s.copyWithUpdatedTime()
	updated.status = newStatus
	updated.version++
	return updated
}

// copyWithUpdatedTime creates a copy of the strategy with updated timestamp.
func (s ForecastingStrategy) copyWithUpdatedTime() ForecastingStrategy {
	updated := s
	// Defensive copy of slice
	updated.inputDatasetCodes = make([]string, len(s.inputDatasetCodes))
	copy(updated.inputDatasetCodes, s.inputDatasetCodes)
	updated.updatedAt = time.Now()
	return updated
}

// Getters for all unexported fields.

// ID returns the internal unique identifier.
func (s ForecastingStrategy) ID() uuid.UUID { return s.id }

// TenantID returns the tenant identifier.
func (s ForecastingStrategy) TenantID() string { return s.tenantID }

// Name returns the strategy name.
func (s ForecastingStrategy) Name() string { return s.name }

// Description returns the strategy description.
func (s ForecastingStrategy) Description() string { return s.description }

// StarlarkCode returns the Starlark script source code.
func (s ForecastingStrategy) StarlarkCode() string { return s.starlarkCode }

// HorizonHours returns how far into the future the forecast extends.
func (s ForecastingStrategy) HorizonHours() int { return s.horizonHours }

// GranularityHours returns the spacing between forecast points.
func (s ForecastingStrategy) GranularityHours() int { return s.granularityHours }

// Schedule returns the cron schedule expression.
func (s ForecastingStrategy) Schedule() string { return s.schedule }

// InputDatasetCodes returns a copy of the input MDS dataset codes.
func (s ForecastingStrategy) InputDatasetCodes() []string {
	codes := make([]string, len(s.inputDatasetCodes))
	copy(codes, s.inputDatasetCodes)
	return codes
}

// OutputDatasetCode returns the MDS dataset code for the forward curve output.
func (s ForecastingStrategy) OutputDatasetCode() string { return s.outputDatasetCode }

// ReferenceDataResolutionKey returns the optional hierarchy node context.
func (s ForecastingStrategy) ReferenceDataResolutionKey() string {
	return s.referenceDataResolutionKey
}

// Status returns the current strategy status.
func (s ForecastingStrategy) Status() StrategyStatus { return s.status }

// Version returns the version number for optimistic locking.
func (s ForecastingStrategy) Version() int64 { return s.version }

// CreatedAt returns the creation timestamp.
func (s ForecastingStrategy) CreatedAt() time.Time { return s.createdAt }

// UpdatedAt returns the last update timestamp.
func (s ForecastingStrategy) UpdatedAt() time.Time { return s.updatedAt }

// ForecastingStrategyBuilder provides a builder pattern for reconstructing
// ForecastingStrategy from the persistence layer. This bypasses normal validation
// since we assume persisted data was already validated.
type ForecastingStrategyBuilder struct {
	strategy ForecastingStrategy
}

// NewForecastingStrategyBuilder creates a new builder for ForecastingStrategy reconstruction.
func NewForecastingStrategyBuilder() *ForecastingStrategyBuilder {
	return &ForecastingStrategyBuilder{
		strategy: ForecastingStrategy{},
	}
}

// WithID sets the unique identifier.
func (b *ForecastingStrategyBuilder) WithID(id uuid.UUID) *ForecastingStrategyBuilder {
	b.strategy.id = id
	return b
}

// WithTenantID sets the tenant identifier.
func (b *ForecastingStrategyBuilder) WithTenantID(tenantID string) *ForecastingStrategyBuilder {
	b.strategy.tenantID = tenantID
	return b
}

// WithName sets the strategy name.
func (b *ForecastingStrategyBuilder) WithName(name string) *ForecastingStrategyBuilder {
	b.strategy.name = name
	return b
}

// WithDescription sets the strategy description.
func (b *ForecastingStrategyBuilder) WithDescription(description string) *ForecastingStrategyBuilder {
	b.strategy.description = description
	return b
}

// WithStarlarkCode sets the Starlark script source.
func (b *ForecastingStrategyBuilder) WithStarlarkCode(code string) *ForecastingStrategyBuilder {
	b.strategy.starlarkCode = code
	return b
}

// WithHorizonHours sets the forecast horizon.
func (b *ForecastingStrategyBuilder) WithHorizonHours(hours int) *ForecastingStrategyBuilder {
	b.strategy.horizonHours = hours
	return b
}

// WithGranularityHours sets the forecast point spacing.
func (b *ForecastingStrategyBuilder) WithGranularityHours(hours int) *ForecastingStrategyBuilder {
	b.strategy.granularityHours = hours
	return b
}

// WithSchedule sets the cron schedule expression.
func (b *ForecastingStrategyBuilder) WithSchedule(schedule string) *ForecastingStrategyBuilder {
	b.strategy.schedule = schedule
	return b
}

// WithInputDatasetCodes sets the input MDS dataset codes.
func (b *ForecastingStrategyBuilder) WithInputDatasetCodes(codes []string) *ForecastingStrategyBuilder {
	b.strategy.inputDatasetCodes = make([]string, len(codes))
	copy(b.strategy.inputDatasetCodes, codes)
	return b
}

// WithOutputDatasetCode sets the output dataset code.
func (b *ForecastingStrategyBuilder) WithOutputDatasetCode(code string) *ForecastingStrategyBuilder {
	b.strategy.outputDatasetCode = code
	return b
}

// WithReferenceDataResolutionKey sets the optional hierarchy node context.
func (b *ForecastingStrategyBuilder) WithReferenceDataResolutionKey(key string) *ForecastingStrategyBuilder {
	b.strategy.referenceDataResolutionKey = key
	return b
}

// WithStatus sets the strategy status.
func (b *ForecastingStrategyBuilder) WithStatus(status StrategyStatus) *ForecastingStrategyBuilder {
	b.strategy.status = status
	return b
}

// WithVersion sets the optimistic locking version.
func (b *ForecastingStrategyBuilder) WithVersion(version int64) *ForecastingStrategyBuilder {
	b.strategy.version = version
	return b
}

// WithCreatedAt sets the creation timestamp.
func (b *ForecastingStrategyBuilder) WithCreatedAt(t time.Time) *ForecastingStrategyBuilder {
	b.strategy.createdAt = t
	return b
}

// WithUpdatedAt sets the last update timestamp.
func (b *ForecastingStrategyBuilder) WithUpdatedAt(t time.Time) *ForecastingStrategyBuilder {
	b.strategy.updatedAt = t
	return b
}

// Build returns the constructed ForecastingStrategy.
// This is used for persistence reconstruction and does not validate.
func (b *ForecastingStrategyBuilder) Build() ForecastingStrategy {
	return b.strategy
}
