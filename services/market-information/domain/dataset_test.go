package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a valid DataSetDefinition for testing
func createValidDataSetDefinition(t *testing.T) DataSetDefinition {
	t.Helper()
	ds, err := NewDataSetDefinition(
		"LBMA_GOLD_PRICE",
		"LBMA Gold Price",
		"London Bullion Market Association Gold Price",
		DataCategoryPricing,
		"price > 0",
		"date + '-' + source",
		"'Invalid price: ' + string(price)",
	)
	require.NoError(t, err)
	return ds
}

func TestNewDataSetDefinition_Success(t *testing.T) {
	beforeCreation := time.Now()

	ds, err := NewDataSetDefinition(
		"LBMA_GOLD_PRICE",
		"LBMA Gold Price",
		"London Bullion Market Association Gold Price",
		DataCategoryPricing,
		"price > 0",
		"date + '-' + source",
		"'Invalid price: ' + string(price)",
	)

	require.NoError(t, err)

	// Verify ID is generated
	assert.NotEqual(t, uuid.Nil, ds.ID())

	// Verify fields are set correctly
	assert.Equal(t, "LBMA_GOLD_PRICE", ds.Code())
	assert.Equal(t, 1, ds.Version())
	assert.Equal(t, "LBMA Gold Price", ds.Name())
	assert.Equal(t, "London Bullion Market Association Gold Price", ds.Description())
	assert.Equal(t, DataCategoryPricing, ds.DataCategory())
	assert.Equal(t, DataSetStatusDraft, ds.Status())
	assert.Equal(t, "price > 0", ds.ValidationExpression())
	assert.Equal(t, "date + '-' + source", ds.ResolutionKeyExpression())
	assert.Equal(t, "'Invalid price: ' + string(price)", ds.ErrorMessageExpression())

	// Verify timestamps
	assert.True(t, ds.CreatedAt().After(beforeCreation) || ds.CreatedAt().Equal(beforeCreation))
	assert.Equal(t, ds.CreatedAt(), ds.UpdatedAt())
	assert.Nil(t, ds.ActivatedAt())
	assert.Nil(t, ds.DeprecatedAt())
}

func TestNewDataSetDefinition_WithContextualCategory(t *testing.T) {
	ds, err := NewDataSetDefinition(
		"MARKET_HOURS",
		"Market Trading Hours",
		"Trading hours for various markets",
		DataCategoryContextual,
		"startTime < endTime",
		"market + '-' + date",
		"",
	)

	require.NoError(t, err)
	assert.Equal(t, DataCategoryContextual, ds.DataCategory())
}

func TestNewDataSetDefinition_EmptyDescription(t *testing.T) {
	ds, err := NewDataSetDefinition(
		"TEST_DATASET",
		"Test Dataset",
		"", // Empty description is allowed
		DataCategoryPricing,
		"value > 0",
		"key",
		"",
	)

	require.NoError(t, err)
	assert.Empty(t, ds.Description())
}

func TestNewDataSetDefinition_EmptyErrorMessageExpression(t *testing.T) {
	ds, err := NewDataSetDefinition(
		"TEST_DATASET",
		"Test Dataset",
		"Test",
		DataCategoryPricing,
		"value > 0",
		"key",
		"", // Empty error message expression is allowed
	)

	require.NoError(t, err)
	assert.Empty(t, ds.ErrorMessageExpression())
}

func TestNewDataSetDefinition_ValidationErrors(t *testing.T) {
	tests := []struct {
		name              string
		code              string
		datasetName       string
		description       string
		category          DataCategory
		validationExpr    string
		resolutionKeyExpr string
		errorMsgExpr      string
		expectedErr       error
	}{
		{
			name:              "empty code",
			code:              "",
			datasetName:       "Test",
			description:       "Test",
			category:          DataCategoryPricing,
			validationExpr:    "valid",
			resolutionKeyExpr: "key",
			errorMsgExpr:      "",
			expectedErr:       ErrCodeRequired,
		},
		{
			name:              "empty name",
			code:              "TEST",
			datasetName:       "",
			description:       "Test",
			category:          DataCategoryPricing,
			validationExpr:    "valid",
			resolutionKeyExpr: "key",
			errorMsgExpr:      "",
			expectedErr:       ErrNameRequired,
		},
		{
			name:              "invalid data category",
			code:              "TEST",
			datasetName:       "Test",
			description:       "Test",
			category:          DataCategory("INVALID"),
			validationExpr:    "valid",
			resolutionKeyExpr: "key",
			errorMsgExpr:      "",
			expectedErr:       ErrInvalidDataCategory,
		},
		{
			name:              "empty validation expression",
			code:              "TEST",
			datasetName:       "Test",
			description:       "Test",
			category:          DataCategoryPricing,
			validationExpr:    "",
			resolutionKeyExpr: "key",
			errorMsgExpr:      "",
			expectedErr:       ErrValidationExpressionRequired,
		},
		{
			name:              "empty resolution key expression",
			code:              "TEST",
			datasetName:       "Test",
			description:       "Test",
			category:          DataCategoryPricing,
			validationExpr:    "valid",
			resolutionKeyExpr: "",
			errorMsgExpr:      "",
			expectedErr:       ErrResolutionKeyExpressionRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDataSetDefinition(
				tt.code,
				tt.datasetName,
				tt.description,
				tt.category,
				tt.validationExpr,
				tt.resolutionKeyExpr,
				tt.errorMsgExpr,
			)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestDataSetDefinition_ActivateDataSet_Success(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	assert.Equal(t, DataSetStatusDraft, ds.Status())

	beforeActivation := time.Now()
	activated, err := ds.ActivateDataSet()

	require.NoError(t, err)
	assert.Equal(t, DataSetStatusActive, activated.Status())
	assert.NotNil(t, activated.ActivatedAt())
	assert.True(t, activated.ActivatedAt().After(beforeActivation) || activated.ActivatedAt().Equal(beforeActivation))
	assert.Nil(t, activated.DeprecatedAt())
	assert.Equal(t, 2, activated.Version())

	// Original should be unchanged (immutability)
	assert.Equal(t, DataSetStatusDraft, ds.Status())
	assert.Equal(t, 1, ds.Version())
}

func TestDataSetDefinition_ActivateDataSet_FromActive_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	activated, err := ds.ActivateDataSet()
	require.NoError(t, err)

	// Try to activate again - should fail because it's already ACTIVE
	_, err = activated.ActivateDataSet()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	assert.Contains(t, err.Error(), "same") // ACTIVE -> ACTIVE is the same status
}

func TestDataSetDefinition_ActivateDataSet_FromDeprecated_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	deprecated, err := ds.DeprecateDataSet()
	require.NoError(t, err)

	// Try to activate deprecated dataset
	_, err = deprecated.ActivateDataSet()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	assert.Contains(t, err.Error(), "DEPRECATED")
}

func TestDataSetDefinition_DeprecateDataSet_FromDraft_Success(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	assert.Equal(t, DataSetStatusDraft, ds.Status())

	beforeDeprecation := time.Now()
	deprecated, err := ds.DeprecateDataSet()

	require.NoError(t, err)
	assert.Equal(t, DataSetStatusDeprecated, deprecated.Status())
	assert.NotNil(t, deprecated.DeprecatedAt())
	assert.True(t, deprecated.DeprecatedAt().After(beforeDeprecation) || deprecated.DeprecatedAt().Equal(beforeDeprecation))
	assert.Nil(t, deprecated.ActivatedAt()) // Never activated
	assert.Equal(t, 2, deprecated.Version())

	// Original should be unchanged (immutability)
	assert.Equal(t, DataSetStatusDraft, ds.Status())
}

func TestDataSetDefinition_DeprecateDataSet_FromActive_Success(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	activated, err := ds.ActivateDataSet()
	require.NoError(t, err)

	beforeDeprecation := time.Now()
	deprecated, err := activated.DeprecateDataSet()

	require.NoError(t, err)
	assert.Equal(t, DataSetStatusDeprecated, deprecated.Status())
	assert.NotNil(t, deprecated.DeprecatedAt())
	assert.True(t, deprecated.DeprecatedAt().After(beforeDeprecation) || deprecated.DeprecatedAt().Equal(beforeDeprecation))
	assert.NotNil(t, deprecated.ActivatedAt()) // Was activated before deprecation
	assert.Equal(t, 3, deprecated.Version())

	// Original should be unchanged (immutability)
	assert.Equal(t, DataSetStatusActive, activated.Status())
}

func TestDataSetDefinition_DeprecateDataSet_FromDeprecated_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	deprecated, err := ds.DeprecateDataSet()
	require.NoError(t, err)

	// Try to deprecate again
	_, err = deprecated.DeprecateDataSet()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestDataSetDefinition_UpdateDescription_Success(t *testing.T) {
	ds := createValidDataSetDefinition(t)

	updated, err := ds.UpdateDescription("New description")

	require.NoError(t, err)
	assert.Equal(t, "New description", updated.Description())
	assert.Equal(t, 2, updated.Version())
	assert.True(t, updated.UpdatedAt().After(ds.UpdatedAt()) || updated.UpdatedAt().Equal(ds.UpdatedAt()))

	// Original should be unchanged
	assert.Equal(t, "London Bullion Market Association Gold Price", ds.Description())
}

func TestDataSetDefinition_UpdateDescription_WhenDeprecated_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	deprecated, err := ds.DeprecateDataSet()
	require.NoError(t, err)

	_, err = deprecated.UpdateDescription("New description")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDataSetDeprecated)
}

func TestDataSetDefinition_UpdateValidationExpression_Success(t *testing.T) {
	ds := createValidDataSetDefinition(t)

	updated, err := ds.UpdateValidationExpression("price >= 0 && price < 10000")

	require.NoError(t, err)
	assert.Equal(t, "price >= 0 && price < 10000", updated.ValidationExpression())
	assert.Equal(t, 2, updated.Version())
}

func TestDataSetDefinition_UpdateValidationExpression_Empty_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)

	_, err := ds.UpdateValidationExpression("")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidationExpressionRequired)
}

func TestDataSetDefinition_UpdateValidationExpression_WhenDeprecated_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	deprecated, err := ds.DeprecateDataSet()
	require.NoError(t, err)

	_, err = deprecated.UpdateValidationExpression("new expr")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDataSetDeprecated)
}

func TestDataSetDefinition_UpdateResolutionKeyExpression_Success(t *testing.T) {
	ds := createValidDataSetDefinition(t)

	updated, err := ds.UpdateResolutionKeyExpression("timestamp + '-' + instrument")

	require.NoError(t, err)
	assert.Equal(t, "timestamp + '-' + instrument", updated.ResolutionKeyExpression())
	assert.Equal(t, 2, updated.Version())
}

func TestDataSetDefinition_UpdateResolutionKeyExpression_Empty_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)

	_, err := ds.UpdateResolutionKeyExpression("")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrResolutionKeyExpressionRequired)
}

func TestDataSetDefinition_UpdateResolutionKeyExpression_WhenDeprecated_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	deprecated, err := ds.DeprecateDataSet()
	require.NoError(t, err)

	_, err = deprecated.UpdateResolutionKeyExpression("new expr")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDataSetDeprecated)
}

func TestDataSetDefinition_UpdateErrorMessageExpression_Success(t *testing.T) {
	ds := createValidDataSetDefinition(t)

	updated, err := ds.UpdateErrorMessageExpression("'Error at ' + timestamp")

	require.NoError(t, err)
	assert.Equal(t, "'Error at ' + timestamp", updated.ErrorMessageExpression())
	assert.Equal(t, 2, updated.Version())
}

func TestDataSetDefinition_UpdateErrorMessageExpression_Empty_Allowed(t *testing.T) {
	ds := createValidDataSetDefinition(t)

	updated, err := ds.UpdateErrorMessageExpression("")

	require.NoError(t, err)
	assert.Empty(t, updated.ErrorMessageExpression())
}

func TestDataSetDefinition_UpdateErrorMessageExpression_WhenDeprecated_Fails(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	deprecated, err := ds.DeprecateDataSet()
	require.NoError(t, err)

	_, err = deprecated.UpdateErrorMessageExpression("new expr")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDataSetDeprecated)
}

func TestDataSetDefinition_Immutability(t *testing.T) {
	ds := createValidDataSetDefinition(t)
	originalID := ds.ID()
	originalCode := ds.Code()
	originalVersion := ds.Version()

	// Perform multiple operations
	activated, _ := ds.ActivateDataSet()
	deprecated, _ := activated.DeprecateDataSet()

	// Original should be completely unchanged
	assert.Equal(t, originalID, ds.ID())
	assert.Equal(t, originalCode, ds.Code())
	assert.Equal(t, originalVersion, ds.Version())
	assert.Equal(t, DataSetStatusDraft, ds.Status())
	assert.Nil(t, ds.ActivatedAt())
	assert.Nil(t, ds.DeprecatedAt())

	// Each step should have its own state
	assert.Equal(t, DataSetStatusActive, activated.Status())
	assert.NotNil(t, activated.ActivatedAt())
	assert.Nil(t, activated.DeprecatedAt())

	assert.Equal(t, DataSetStatusDeprecated, deprecated.Status())
	assert.NotNil(t, deprecated.ActivatedAt())
	assert.NotNil(t, deprecated.DeprecatedAt())
}

func TestDataSetDefinition_FullLifecycle(t *testing.T) {
	// Create new dataset
	ds, err := NewDataSetDefinition(
		"LIFECYCLE_TEST",
		"Lifecycle Test Dataset",
		"Testing full lifecycle",
		DataCategoryPricing,
		"true",
		"id",
		"",
	)
	require.NoError(t, err)
	assert.Equal(t, DataSetStatusDraft, ds.Status())
	assert.Equal(t, 1, ds.Version())

	// Update description
	ds, err = ds.UpdateDescription("Updated description")
	require.NoError(t, err)
	assert.Equal(t, 2, ds.Version())

	// Activate
	ds, err = ds.ActivateDataSet()
	require.NoError(t, err)
	assert.Equal(t, DataSetStatusActive, ds.Status())
	assert.Equal(t, 3, ds.Version())
	assert.NotNil(t, ds.ActivatedAt())

	// Deprecate
	ds, err = ds.DeprecateDataSet()
	require.NoError(t, err)
	assert.Equal(t, DataSetStatusDeprecated, ds.Status())
	assert.Equal(t, 4, ds.Version())
	assert.NotNil(t, ds.DeprecatedAt())

	// Cannot update after deprecation
	_, err = ds.UpdateDescription("Should fail")
	assert.ErrorIs(t, err, ErrDataSetDeprecated)
}

func TestDataSetDefinitionBuilder_Reconstruction(t *testing.T) {
	id := uuid.New()
	now := time.Now()
	activatedAt := now.Add(-24 * time.Hour)
	deprecatedAt := now

	ds := NewDataSetDefinitionBuilder().
		WithID(id).
		WithCode("RECONSTRUCTED").
		WithVersion(5).
		WithName("Reconstructed Dataset").
		WithDescription("From persistence").
		WithDataCategory(DataCategoryContextual).
		WithStatus(DataSetStatusDeprecated).
		WithValidationExpression("valid").
		WithResolutionKeyExpression("key").
		WithErrorMessageExpression("error").
		WithCreatedAt(now.Add(-48 * time.Hour)).
		WithUpdatedAt(now).
		WithActivatedAt(&activatedAt).
		WithDeprecatedAt(&deprecatedAt).
		Build()

	assert.Equal(t, id, ds.ID())
	assert.Equal(t, "RECONSTRUCTED", ds.Code())
	assert.Equal(t, 5, ds.Version())
	assert.Equal(t, "Reconstructed Dataset", ds.Name())
	assert.Equal(t, "From persistence", ds.Description())
	assert.Equal(t, DataCategoryContextual, ds.DataCategory())
	assert.Equal(t, DataSetStatusDeprecated, ds.Status())
	assert.Equal(t, "valid", ds.ValidationExpression())
	assert.Equal(t, "key", ds.ResolutionKeyExpression())
	assert.Equal(t, "error", ds.ErrorMessageExpression())
	assert.NotNil(t, ds.ActivatedAt())
	assert.NotNil(t, ds.DeprecatedAt())
}

func TestDataSetDefinitionBuilder_PartialReconstruction(t *testing.T) {
	id := uuid.New()

	ds := NewDataSetDefinitionBuilder().
		WithID(id).
		WithCode("PARTIAL").
		WithStatus(DataSetStatusDraft).
		Build()

	assert.Equal(t, id, ds.ID())
	assert.Equal(t, "PARTIAL", ds.Code())
	assert.Equal(t, DataSetStatusDraft, ds.Status())
	assert.Empty(t, ds.Name())
	assert.Nil(t, ds.ActivatedAt())
	assert.Nil(t, ds.DeprecatedAt())
}

func TestDataSetDefinition_UniqueIDs(t *testing.T) {
	// Create multiple datasets and verify each has a unique ID
	ids := make(map[uuid.UUID]bool)

	for range 100 {
		ds, err := NewDataSetDefinition(
			"TEST",
			"Test",
			"",
			DataCategoryPricing,
			"true",
			"id",
			"",
		)
		require.NoError(t, err)
		assert.False(t, ids[ds.ID()], "Duplicate ID generated")
		ids[ds.ID()] = true
	}
}

// Tests for DataCategory enum

func TestDataCategory_IsValid(t *testing.T) {
	tests := []struct {
		category DataCategory
		expected bool
	}{
		{DataCategoryPricing, true},
		{DataCategoryContextual, true},
		{DataCategory("INVALID"), false},
		{DataCategory(""), false},
		{DataCategory("pricing"), false}, // Case sensitive
	}

	for _, tt := range tests {
		t.Run(string(tt.category), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.category.IsValid())
		})
	}
}

func TestDataCategory_String(t *testing.T) {
	assert.Equal(t, "PRICING", DataCategoryPricing.String())
	assert.Equal(t, "CONTEXTUAL", DataCategoryContextual.String())
}

// Tests for DataSetStatus enum

func TestDataSetStatus_IsValid(t *testing.T) {
	tests := []struct {
		status   DataSetStatus
		expected bool
	}{
		{DataSetStatusDraft, true},
		{DataSetStatusActive, true},
		{DataSetStatusDeprecated, true},
		{DataSetStatus("INVALID"), false},
		{DataSetStatus(""), false},
		{DataSetStatus("draft"), false}, // Case sensitive
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsValid())
		})
	}
}

func TestDataSetStatus_String(t *testing.T) {
	assert.Equal(t, "DRAFT", DataSetStatusDraft.String())
	assert.Equal(t, "ACTIVE", DataSetStatusActive.String())
	assert.Equal(t, "DEPRECATED", DataSetStatusDeprecated.String())
}

func TestDataSetStatus_ValidTransitions(t *testing.T) {
	tests := []struct {
		name   string
		from   DataSetStatus
		to     DataSetStatus
		reason string
	}{
		{
			name:   "DRAFT to ACTIVE",
			from:   DataSetStatusDraft,
			to:     DataSetStatusActive,
			reason: "Activating draft dataset should be allowed",
		},
		{
			name:   "DRAFT to DEPRECATED",
			from:   DataSetStatusDraft,
			to:     DataSetStatusDeprecated,
			reason: "Deprecating draft dataset should be allowed",
		},
		{
			name:   "ACTIVE to DEPRECATED",
			from:   DataSetStatusActive,
			to:     DataSetStatusDeprecated,
			reason: "Deprecating active dataset should be allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.from.CanTransitionTo(tt.to), tt.reason)
			err := ValidateStatusTransition(tt.from, tt.to)
			assert.NoError(t, err, tt.reason)
		})
	}
}

func TestDataSetStatus_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name   string
		from   DataSetStatus
		to     DataSetStatus
		reason string
	}{
		{
			name:   "ACTIVE to DRAFT",
			from:   DataSetStatusActive,
			to:     DataSetStatusDraft,
			reason: "Reverting active to draft should not be allowed",
		},
		{
			name:   "DEPRECATED to DRAFT",
			from:   DataSetStatusDeprecated,
			to:     DataSetStatusDraft,
			reason: "Reverting deprecated to draft should not be allowed",
		},
		{
			name:   "DEPRECATED to ACTIVE",
			from:   DataSetStatusDeprecated,
			to:     DataSetStatusActive,
			reason: "Reactivating deprecated dataset should not be allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.from.CanTransitionTo(tt.to), tt.reason)
			err := ValidateStatusTransition(tt.from, tt.to)
			assert.Error(t, err, tt.reason)
			assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
		})
	}
}

func TestDataSetStatus_TerminalState(t *testing.T) {
	// DEPRECATED is a terminal state - no transitions should be allowed from it
	deprecatedStatus := DataSetStatusDeprecated

	t.Run("cannot transition to DRAFT", func(t *testing.T) {
		assert.False(t, deprecatedStatus.CanTransitionTo(DataSetStatusDraft))
		err := ValidateStatusTransition(deprecatedStatus, DataSetStatusDraft)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
	})

	t.Run("cannot transition to ACTIVE", func(t *testing.T) {
		assert.False(t, deprecatedStatus.CanTransitionTo(DataSetStatusActive))
		err := ValidateStatusTransition(deprecatedStatus, DataSetStatusActive)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
	})

	t.Run("cannot transition to DEPRECATED (same state)", func(t *testing.T) {
		assert.False(t, deprecatedStatus.CanTransitionTo(DataSetStatusDeprecated))
		err := ValidateStatusTransition(deprecatedStatus, DataSetStatusDeprecated)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
		assert.Contains(t, err.Error(), "source and target status are the same")
	})
}

func TestDataSetStatus_SameStatusTransition(t *testing.T) {
	// Transitioning to the same status should be rejected
	tests := []struct {
		name   string
		status DataSetStatus
	}{
		{"DRAFT to DRAFT", DataSetStatusDraft},
		{"ACTIVE to ACTIVE", DataSetStatusActive},
		{"DEPRECATED to DEPRECATED", DataSetStatusDeprecated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.status.CanTransitionTo(tt.status))
			err := ValidateStatusTransition(tt.status, tt.status)
			assert.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidStatusTransition))
			assert.Contains(t, err.Error(), "source and target status are the same")
		})
	}
}

func TestDataSetStatus_AllValidTransitionsMatrix(t *testing.T) {
	// Comprehensive matrix test for all possible transitions
	statuses := []DataSetStatus{
		DataSetStatusDraft,
		DataSetStatusActive,
		DataSetStatusDeprecated,
	}

	expectedTransitions := map[DataSetStatus]map[DataSetStatus]bool{
		DataSetStatusDraft: {
			DataSetStatusDraft:      false, // same status
			DataSetStatusActive:     true,
			DataSetStatusDeprecated: true,
		},
		DataSetStatusActive: {
			DataSetStatusDraft:      false,
			DataSetStatusActive:     false, // same status
			DataSetStatusDeprecated: true,
		},
		DataSetStatusDeprecated: {
			DataSetStatusDraft:      false,
			DataSetStatusActive:     false,
			DataSetStatusDeprecated: false, // same status
		},
	}

	for _, from := range statuses {
		for _, to := range statuses {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				expected := expectedTransitions[from][to]
				actual := from.CanTransitionTo(to)
				assert.Equal(t, expected, actual,
					"unexpected transition result from %s to %s", from, to)
			})
		}
	}
}

func TestDataSetStatus_UnknownStatus(t *testing.T) {
	// Test behavior with an unknown/invalid status
	unknownStatus := DataSetStatus("UNKNOWN")

	t.Run("unknown status cannot transition to valid status", func(t *testing.T) {
		assert.False(t, unknownStatus.CanTransitionTo(DataSetStatusDraft))
		assert.False(t, unknownStatus.CanTransitionTo(DataSetStatusActive))
		assert.False(t, unknownStatus.CanTransitionTo(DataSetStatusDeprecated))
	})

	t.Run("valid status cannot transition to unknown status", func(t *testing.T) {
		assert.False(t, DataSetStatusDraft.CanTransitionTo(unknownStatus))
		assert.False(t, DataSetStatusActive.CanTransitionTo(unknownStatus))
	})
}

func TestValidateStatusTransition_ErrorMessages(t *testing.T) {
	t.Run("same status error contains indication", func(t *testing.T) {
		err := ValidateStatusTransition(DataSetStatusDraft, DataSetStatusDraft)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "same")
	})

	t.Run("invalid transition error contains both statuses", func(t *testing.T) {
		err := ValidateStatusTransition(DataSetStatusDeprecated, DataSetStatusActive)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DEPRECATED")
		assert.Contains(t, err.Error(), "ACTIVE")
	})
}

func TestDataSetStatus_Constants(t *testing.T) {
	// Verify the constant values are as expected
	assert.Equal(t, DataSetStatus("DRAFT"), DataSetStatusDraft)
	assert.Equal(t, DataSetStatus("ACTIVE"), DataSetStatusActive)
	assert.Equal(t, DataSetStatus("DEPRECATED"), DataSetStatusDeprecated)
}

func TestValidateStatusTransition_ReturnsNilOnSuccess(t *testing.T) {
	validTransitions := []struct {
		from DataSetStatus
		to   DataSetStatus
	}{
		{DataSetStatusDraft, DataSetStatusActive},
		{DataSetStatusDraft, DataSetStatusDeprecated},
		{DataSetStatusActive, DataSetStatusDeprecated},
	}

	for _, tt := range validTransitions {
		t.Run(string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			err := ValidateStatusTransition(tt.from, tt.to)
			assert.NoError(t, err, "expected no error for valid transition from %s to %s", tt.from, tt.to)
		})
	}
}
