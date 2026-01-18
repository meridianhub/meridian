// Package domain contains the domain models for the Market Information service.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// DataSetDefinition represents a dataset definition aggregate root.
// This is an immutable value type - all modification methods return a new instance.
//
// Dataset definitions configure how market data is validated, keyed, and processed.
// Each definition includes CEL expressions for:
// - Validation: Ensuring data integrity
// - Resolution Key: Extracting unique keys for data point identification
// - Error Message: Generating user-friendly error messages
//
// Lifecycle states:
// - DRAFT: Initial state, dataset is being configured
// - ACTIVE: Dataset is ready for production use
// - DEPRECATED: Dataset is retired (terminal state)
type DataSetDefinition struct {
	id                      uuid.UUID
	code                    string // Unique business identifier like 'LBMA_GOLD_PRICE'
	version                 int
	name                    string
	description             string
	dataCategory            DataCategory
	status                  DataSetStatus
	validationExpression    string // CEL expression for data validation
	resolutionKeyExpression string // CEL expression for extracting resolution key
	errorMessageExpression  string // CEL expression for error message generation
	createdAt               time.Time
	updatedAt               time.Time
	activatedAt             *time.Time // Set when transitioning to ACTIVE
	deprecatedAt            *time.Time // Set when transitioning to DEPRECATED
}

// NewDataSetDefinition creates a new DataSetDefinition with validated fields.
// Returns a value type (not pointer) following the immutability pattern.
//
// Initial state:
//   - Status: DRAFT
//   - Version: 1
//   - ActivatedAt: nil
//   - DeprecatedAt: nil
//
// Validation:
//   - code cannot be empty
//   - name cannot be empty
//   - dataCategory must be valid
//   - validationExpression cannot be empty
//   - resolutionKeyExpression cannot be empty
func NewDataSetDefinition(
	code, name, description string,
	dataCategory DataCategory,
	validationExpression, resolutionKeyExpression, errorMessageExpression string,
) (DataSetDefinition, error) {
	if code == "" {
		return DataSetDefinition{}, ErrCodeRequired
	}
	if name == "" {
		return DataSetDefinition{}, ErrNameRequired
	}
	if !dataCategory.IsValid() {
		return DataSetDefinition{}, ErrInvalidDataCategory
	}
	if validationExpression == "" {
		return DataSetDefinition{}, ErrValidationExpressionRequired
	}
	if resolutionKeyExpression == "" {
		return DataSetDefinition{}, ErrResolutionKeyExpressionRequired
	}

	now := time.Now()
	return DataSetDefinition{
		id:                      uuid.New(),
		code:                    code,
		version:                 1,
		name:                    name,
		description:             description,
		dataCategory:            dataCategory,
		status:                  DataSetStatusDraft,
		validationExpression:    validationExpression,
		resolutionKeyExpression: resolutionKeyExpression,
		errorMessageExpression:  errorMessageExpression,
		createdAt:               now,
		updatedAt:               now,
		activatedAt:             nil,
		deprecatedAt:            nil,
	}, nil
}

// ActivateDataSet transitions the dataset to ACTIVE status.
// Returns a new instance with updated status and activatedAt timestamp.
// Returns error if the transition is not valid (only DRAFT → ACTIVE is allowed).
func (d DataSetDefinition) ActivateDataSet() (DataSetDefinition, error) {
	if err := ValidateStatusTransition(d.status, DataSetStatusActive); err != nil {
		return d, err
	}
	return d.withStatusChange(DataSetStatusActive), nil
}

// DeprecateDataSet transitions the dataset to DEPRECATED status.
// Returns a new instance with updated status and deprecatedAt timestamp.
// Returns error if the transition is not valid.
// Valid transitions: DRAFT → DEPRECATED, ACTIVE → DEPRECATED
func (d DataSetDefinition) DeprecateDataSet() (DataSetDefinition, error) {
	if err := ValidateStatusTransition(d.status, DataSetStatusDeprecated); err != nil {
		return d, err
	}
	return d.withStatusChange(DataSetStatusDeprecated), nil
}

// UpdateDescription updates the dataset description.
// Returns a new instance with updated description.
// Returns error if dataset is deprecated.
func (d DataSetDefinition) UpdateDescription(newDescription string) (DataSetDefinition, error) {
	if d.status == DataSetStatusDeprecated {
		return d, ErrDataSetDeprecated
	}

	newDataSet := d.copyWithUpdatedTime()
	newDataSet.description = newDescription
	newDataSet.version++
	return newDataSet, nil
}

// UpdateValidationExpression updates the validation CEL expression.
// Returns a new instance with updated expression.
// Returns error if dataset is deprecated or expression is empty.
func (d DataSetDefinition) UpdateValidationExpression(expression string) (DataSetDefinition, error) {
	if d.status == DataSetStatusDeprecated {
		return d, ErrDataSetDeprecated
	}
	if expression == "" {
		return d, ErrValidationExpressionRequired
	}

	newDataSet := d.copyWithUpdatedTime()
	newDataSet.validationExpression = expression
	newDataSet.version++
	return newDataSet, nil
}

// UpdateResolutionKeyExpression updates the resolution key CEL expression.
// Returns a new instance with updated expression.
// Returns error if dataset is deprecated or expression is empty.
func (d DataSetDefinition) UpdateResolutionKeyExpression(expression string) (DataSetDefinition, error) {
	if d.status == DataSetStatusDeprecated {
		return d, ErrDataSetDeprecated
	}
	if expression == "" {
		return d, ErrResolutionKeyExpressionRequired
	}

	newDataSet := d.copyWithUpdatedTime()
	newDataSet.resolutionKeyExpression = expression
	newDataSet.version++
	return newDataSet, nil
}

// UpdateErrorMessageExpression updates the error message CEL expression.
// Returns a new instance with updated expression.
// Returns error if dataset is deprecated.
func (d DataSetDefinition) UpdateErrorMessageExpression(expression string) (DataSetDefinition, error) {
	if d.status == DataSetStatusDeprecated {
		return d, ErrDataSetDeprecated
	}

	newDataSet := d.copyWithUpdatedTime()
	newDataSet.errorMessageExpression = expression
	newDataSet.version++
	return newDataSet, nil
}

// withStatusChange creates a new instance with updated status.
// This is a private helper that handles the immutable update pattern.
func (d DataSetDefinition) withStatusChange(newStatus DataSetStatus) DataSetDefinition {
	newDataSet := d.copyWithUpdatedTime()
	newDataSet.status = newStatus
	newDataSet.version++

	now := newDataSet.updatedAt
	switch newStatus {
	case DataSetStatusDraft:
		// DRAFT is the initial state - no timestamp to set
	case DataSetStatusActive:
		newDataSet.activatedAt = &now
	case DataSetStatusDeprecated:
		newDataSet.deprecatedAt = &now
	}

	return newDataSet
}

// copyWithUpdatedTime creates a copy of the dataset with updated timestamp.
func (d DataSetDefinition) copyWithUpdatedTime() DataSetDefinition {
	newDataSet := d
	newDataSet.updatedAt = time.Now()
	return newDataSet
}

// Getters for all unexported fields.

// ID returns the internal unique identifier.
func (d DataSetDefinition) ID() uuid.UUID {
	return d.id
}

// Code returns the unique business identifier (e.g., 'LBMA_GOLD_PRICE').
func (d DataSetDefinition) Code() string {
	return d.code
}

// Version returns the version number for optimistic locking.
func (d DataSetDefinition) Version() int {
	return d.version
}

// Name returns the display name.
func (d DataSetDefinition) Name() string {
	return d.name
}

// Description returns the dataset description.
func (d DataSetDefinition) Description() string {
	return d.description
}

// DataCategory returns the data category.
func (d DataSetDefinition) DataCategory() DataCategory {
	return d.dataCategory
}

// Status returns the current dataset status.
func (d DataSetDefinition) Status() DataSetStatus {
	return d.status
}

// ValidationExpression returns the CEL expression for data validation.
func (d DataSetDefinition) ValidationExpression() string {
	return d.validationExpression
}

// ResolutionKeyExpression returns the CEL expression for extracting resolution key.
func (d DataSetDefinition) ResolutionKeyExpression() string {
	return d.resolutionKeyExpression
}

// ErrorMessageExpression returns the CEL expression for error message generation.
func (d DataSetDefinition) ErrorMessageExpression() string {
	return d.errorMessageExpression
}

// CreatedAt returns the creation timestamp.
func (d DataSetDefinition) CreatedAt() time.Time {
	return d.createdAt
}

// UpdatedAt returns the last update timestamp.
func (d DataSetDefinition) UpdatedAt() time.Time {
	return d.updatedAt
}

// ActivatedAt returns the activation timestamp.
// Returns nil if the dataset has not been activated.
func (d DataSetDefinition) ActivatedAt() *time.Time {
	return d.activatedAt
}

// DeprecatedAt returns the deprecation timestamp.
// Returns nil if the dataset has not been deprecated.
func (d DataSetDefinition) DeprecatedAt() *time.Time {
	return d.deprecatedAt
}

// DataSetDefinitionBuilder provides a builder pattern for reconstructing
// DataSetDefinition from persistence layer. This bypasses normal validation
// since we assume persisted data was already validated.
type DataSetDefinitionBuilder struct {
	dataset DataSetDefinition
}

// NewDataSetDefinitionBuilder creates a new builder for DataSetDefinition reconstruction.
func NewDataSetDefinitionBuilder() *DataSetDefinitionBuilder {
	return &DataSetDefinitionBuilder{
		dataset: DataSetDefinition{},
	}
}

// WithID sets the internal unique identifier.
func (b *DataSetDefinitionBuilder) WithID(id uuid.UUID) *DataSetDefinitionBuilder {
	b.dataset.id = id
	return b
}

// WithCode sets the unique business identifier.
func (b *DataSetDefinitionBuilder) WithCode(code string) *DataSetDefinitionBuilder {
	b.dataset.code = code
	return b
}

// WithVersion sets the version number.
func (b *DataSetDefinitionBuilder) WithVersion(version int) *DataSetDefinitionBuilder {
	b.dataset.version = version
	return b
}

// WithName sets the display name.
func (b *DataSetDefinitionBuilder) WithName(name string) *DataSetDefinitionBuilder {
	b.dataset.name = name
	return b
}

// WithDescription sets the description.
func (b *DataSetDefinitionBuilder) WithDescription(description string) *DataSetDefinitionBuilder {
	b.dataset.description = description
	return b
}

// WithDataCategory sets the data category.
func (b *DataSetDefinitionBuilder) WithDataCategory(category DataCategory) *DataSetDefinitionBuilder {
	b.dataset.dataCategory = category
	return b
}

// WithStatus sets the dataset status.
func (b *DataSetDefinitionBuilder) WithStatus(status DataSetStatus) *DataSetDefinitionBuilder {
	b.dataset.status = status
	return b
}

// WithValidationExpression sets the validation CEL expression.
func (b *DataSetDefinitionBuilder) WithValidationExpression(expression string) *DataSetDefinitionBuilder {
	b.dataset.validationExpression = expression
	return b
}

// WithResolutionKeyExpression sets the resolution key CEL expression.
func (b *DataSetDefinitionBuilder) WithResolutionKeyExpression(expression string) *DataSetDefinitionBuilder {
	b.dataset.resolutionKeyExpression = expression
	return b
}

// WithErrorMessageExpression sets the error message CEL expression.
func (b *DataSetDefinitionBuilder) WithErrorMessageExpression(expression string) *DataSetDefinitionBuilder {
	b.dataset.errorMessageExpression = expression
	return b
}

// WithCreatedAt sets the creation timestamp.
func (b *DataSetDefinitionBuilder) WithCreatedAt(createdAt time.Time) *DataSetDefinitionBuilder {
	b.dataset.createdAt = createdAt
	return b
}

// WithUpdatedAt sets the last update timestamp.
func (b *DataSetDefinitionBuilder) WithUpdatedAt(updatedAt time.Time) *DataSetDefinitionBuilder {
	b.dataset.updatedAt = updatedAt
	return b
}

// WithActivatedAt sets the activation timestamp.
func (b *DataSetDefinitionBuilder) WithActivatedAt(activatedAt *time.Time) *DataSetDefinitionBuilder {
	b.dataset.activatedAt = activatedAt
	return b
}

// WithDeprecatedAt sets the deprecation timestamp.
func (b *DataSetDefinitionBuilder) WithDeprecatedAt(deprecatedAt *time.Time) *DataSetDefinitionBuilder {
	b.dataset.deprecatedAt = deprecatedAt
	return b
}

// Build returns the constructed DataSetDefinition.
// This is used for persistence reconstruction and does not validate.
func (b *DataSetDefinitionBuilder) Build() DataSetDefinition {
	return b.dataset
}
