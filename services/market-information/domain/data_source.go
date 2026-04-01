// Package domain contains the core business logic for market information.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// DataSourceStatus represents the lifecycle state of a data source.
type DataSourceStatus string

const (
	// DataSourceStatusActive means the data source is available for use.
	DataSourceStatusActive DataSourceStatus = "ACTIVE"

	// DataSourceStatusDeprecated means the data source is no longer recommended for new use.
	DataSourceStatusDeprecated DataSourceStatus = "DEPRECATED"
)

// IsValid returns true if the data source status is a recognized valid value.
func (s DataSourceStatus) IsValid() bool {
	return s == DataSourceStatusActive || s == DataSourceStatusDeprecated
}

// String returns the string representation of the data source status.
func (s DataSourceStatus) String() string {
	return string(s)
}

// DataSource errors.
var (
	// ErrDataSourceCodeRequired is returned when the code is empty.
	ErrDataSourceCodeRequired = errors.New("data source code is required")

	// ErrDataSourceNameRequired is returned when the name is empty.
	ErrDataSourceNameRequired = errors.New("data source name is required")

	// ErrInvalidSourceType is returned when the source type is not valid.
	ErrInvalidSourceType = errors.New("invalid source type")

	// ErrInvalidTrustLevel is returned when the trust level is outside the valid range (0-100).
	ErrInvalidTrustLevel = errors.New("trust level must be between 0 and 100")

	// ErrDataSourceNotActive is returned when trying to deprecate a data source that is not ACTIVE.
	ErrDataSourceNotActive = errors.New("data source is not in ACTIVE status")

	// ErrDataSourceAlreadyDeprecated is returned when trying to deprecate an already deprecated data source.
	ErrDataSourceAlreadyDeprecated = errors.New("data source is already deprecated")
)

// SourceType represents the type of data source.
type SourceType string

// Source type constants.
const (
	// SourceTypeAPI indicates data received from an external API.
	SourceTypeAPI SourceType = "API"

	// SourceTypeManual indicates data entered manually by operators.
	SourceTypeManual SourceType = "MANUAL"

	// SourceTypeScheduled indicates data from scheduled batch processes.
	SourceTypeScheduled SourceType = "SCHEDULED"
)

// validSourceTypes contains all valid source types for efficient lookup.
var validSourceTypes = map[SourceType]bool{
	SourceTypeAPI:       true,
	SourceTypeManual:    true,
	SourceTypeScheduled: true,
}

// IsValid returns true if the source type is a recognized valid type.
func (s SourceType) IsValid() bool {
	return validSourceTypes[s]
}

// String returns the string representation of the source type.
func (s SourceType) String() string {
	return string(s)
}

// DataSource represents a source of market information data.
// This entity tracks the configuration and metadata for external data feeds,
// manual entry points, and scheduled data imports.
//
// The TrustLevel field (0-100) is used in the Time-Bound Quality Ladder to determine
// precedence when multiple sources provide data for the same instrument and time period.
// Higher trust levels indicate more reliable sources.
type DataSource struct {
	id           uuid.UUID
	code         string // Unique business identifier (e.g., "LBMA", "BLOOMBERG", "MANUAL_ENTRY")
	name         string // Display name
	description  string // Optional detailed description
	sourceType   SourceType
	trustLevel   int              // 0-100, higher values indicate more trusted sources
	isActive     bool             // Whether this source is currently accepting data
	status       DataSourceStatus // Lifecycle state (ACTIVE or DEPRECATED)
	createdAt    time.Time
	updatedAt    time.Time
	deprecatedAt *time.Time // When this source was deprecated
}

// NewDataSource creates a new DataSource with validated fields.
// Returns a value type (not pointer) following the immutability pattern.
//
// Parameters:
//   - code: Unique business identifier for the data source (required)
//   - name: Display name for the data source (required)
//   - description: Optional detailed description
//   - sourceType: Type of source (API, MANUAL, SCHEDULED)
//   - trustLevel: Trust level from 0-100 for quality precedence
//
// Initial state:
//   - IsActive: true
//   - CreatedAt/UpdatedAt: current time
//
// Validation:
//   - code and name cannot be empty
//   - sourceType must be valid (API, MANUAL, SCHEDULED)
//   - trustLevel must be between 0 and 100 inclusive
func NewDataSource(
	code, name, description string,
	sourceType SourceType,
	trustLevel int,
) (DataSource, error) {
	if code == "" {
		return DataSource{}, ErrDataSourceCodeRequired
	}
	if name == "" {
		return DataSource{}, ErrDataSourceNameRequired
	}
	if !sourceType.IsValid() {
		return DataSource{}, ErrInvalidSourceType
	}
	if trustLevel < 0 || trustLevel > 100 {
		return DataSource{}, ErrInvalidTrustLevel
	}

	now := time.Now()
	return DataSource{
		id:          uuid.New(),
		code:        code,
		name:        name,
		description: description,
		sourceType:  sourceType,
		trustLevel:  trustLevel,
		isActive:    true,
		status:      DataSourceStatusActive,
		createdAt:   now,
		updatedAt:   now,
	}, nil
}

// Getters for all unexported fields.

// ID returns the internal unique identifier.
func (d DataSource) ID() uuid.UUID {
	return d.id
}

// Code returns the unique business identifier.
func (d DataSource) Code() string {
	return d.code
}

// Name returns the display name.
func (d DataSource) Name() string {
	return d.name
}

// Description returns the detailed description.
func (d DataSource) Description() string {
	return d.description
}

// SourceType returns the type of data source.
func (d DataSource) SourceType() SourceType {
	return d.sourceType
}

// TrustLevel returns the trust level (0-100) for quality precedence.
func (d DataSource) TrustLevel() int {
	return d.trustLevel
}

// IsActive returns whether this source is currently accepting data.
func (d DataSource) IsActive() bool {
	return d.isActive
}

// CreatedAt returns the creation timestamp.
func (d DataSource) CreatedAt() time.Time {
	return d.createdAt
}

// UpdatedAt returns the last update timestamp.
func (d DataSource) UpdatedAt() time.Time {
	return d.updatedAt
}

// Status returns the lifecycle status of this data source.
func (d DataSource) Status() DataSourceStatus {
	return d.status
}

// DeprecatedAt returns the deprecation timestamp, or nil if not deprecated.
func (d DataSource) DeprecatedAt() *time.Time {
	return d.deprecatedAt
}

// DataSourceBuilder provides a builder pattern for reconstructing
// DataSource from persistence layer. This bypasses normal validation
// since we assume persisted data was already validated.
type DataSourceBuilder struct {
	source DataSource
}

// NewDataSourceBuilder creates a new builder for DataSource reconstruction.
func NewDataSourceBuilder() *DataSourceBuilder {
	return &DataSourceBuilder{
		source: DataSource{},
	}
}

// WithID sets the internal unique identifier.
func (b *DataSourceBuilder) WithID(id uuid.UUID) *DataSourceBuilder {
	b.source.id = id
	return b
}

// WithCode sets the unique business identifier.
func (b *DataSourceBuilder) WithCode(code string) *DataSourceBuilder {
	b.source.code = code
	return b
}

// WithName sets the display name.
func (b *DataSourceBuilder) WithName(name string) *DataSourceBuilder {
	b.source.name = name
	return b
}

// WithDescription sets the detailed description.
func (b *DataSourceBuilder) WithDescription(description string) *DataSourceBuilder {
	b.source.description = description
	return b
}

// WithSourceType sets the source type.
func (b *DataSourceBuilder) WithSourceType(sourceType SourceType) *DataSourceBuilder {
	b.source.sourceType = sourceType
	return b
}

// WithTrustLevel sets the trust level.
func (b *DataSourceBuilder) WithTrustLevel(trustLevel int) *DataSourceBuilder {
	b.source.trustLevel = trustLevel
	return b
}

// WithIsActive sets the active status.
func (b *DataSourceBuilder) WithIsActive(isActive bool) *DataSourceBuilder {
	b.source.isActive = isActive
	return b
}

// WithCreatedAt sets the creation timestamp.
func (b *DataSourceBuilder) WithCreatedAt(createdAt time.Time) *DataSourceBuilder {
	b.source.createdAt = createdAt
	return b
}

// WithUpdatedAt sets the last update timestamp.
func (b *DataSourceBuilder) WithUpdatedAt(updatedAt time.Time) *DataSourceBuilder {
	b.source.updatedAt = updatedAt
	return b
}

// WithStatus sets the lifecycle status.
func (b *DataSourceBuilder) WithStatus(status DataSourceStatus) *DataSourceBuilder {
	b.source.status = status
	return b
}

// WithDeprecatedAt sets the deprecation timestamp.
func (b *DataSourceBuilder) WithDeprecatedAt(deprecatedAt *time.Time) *DataSourceBuilder {
	b.source.deprecatedAt = deprecatedAt
	return b
}

// Build returns the constructed DataSource.
// This is used for persistence reconstruction and does not validate.
func (b *DataSourceBuilder) Build() DataSource {
	return b.source
}
