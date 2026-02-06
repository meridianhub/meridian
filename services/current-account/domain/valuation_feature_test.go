package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewValuationFeature(t *testing.T) {
	accountID := uuid.New()
	methodID := uuid.New()
	instrumentCode := "USD"
	methodVersion := 1
	parameters := map[string]interface{}{
		"source": "ECB",
		"lag":    "1D",
	}
	createdBy := "test-user"

	feature, err := NewValuationFeature(accountID, instrumentCode, methodID, methodVersion, parameters, createdBy)

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, feature.ID)
	assert.Equal(t, accountID, feature.AccountID)
	assert.Equal(t, instrumentCode, feature.InstrumentCode)
	assert.Equal(t, methodID, feature.ValuationMethodID)
	assert.Equal(t, methodVersion, feature.ValuationMethodVersion)
	assert.Equal(t, parameters, feature.Parameters)
	assert.Equal(t, ValuationFeatureLifecycleStatusInitiated, feature.LifecycleStatus)
	assert.Equal(t, createdBy, feature.CreatedBy)
	assert.Equal(t, createdBy, feature.UpdatedBy)
	assert.Equal(t, 1, feature.Version)
	assert.False(t, feature.ValidFrom.IsZero())
	assert.True(t, feature.ValidTo.Year() == 9999) // Max timestamp
}

func TestNewValuationFeature_EmptyInstrumentCode(t *testing.T) {
	accountID := uuid.New()
	methodID := uuid.New()

	_, err := NewValuationFeature(accountID, "", methodID, 1, nil, "test-user")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentCodeEmpty)
}

func TestValuationFeature_Activate(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	updatedBy := "activator"
	err = feature.Activate(updatedBy)

	require.NoError(t, err)
	assert.Equal(t, ValuationFeatureLifecycleStatusActive, feature.LifecycleStatus)
	assert.Equal(t, updatedBy, feature.UpdatedBy)
	assert.True(t, feature.IsActive())
}

func TestValuationFeature_Activate_Idempotent(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	// First activation
	err = feature.Activate("user1")
	require.NoError(t, err)

	// Second activation - should be idempotent
	err = feature.Activate("user2")
	require.NoError(t, err)
	assert.Equal(t, ValuationFeatureLifecycleStatusActive, feature.LifecycleStatus)
}

func TestValuationFeature_Activate_InvalidTransition(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	// Terminate the feature first
	err = feature.Activate("user1")
	require.NoError(t, err)
	err = feature.Terminate("user1")
	require.NoError(t, err)

	// Try to activate a terminated feature - should fail
	err = feature.Activate("user2")
	require.Error(t, err)
	assert.Equal(t, ErrInvalidValuationFeatureTransition, err)
}

func TestValuationFeature_Terminate(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	// Activate first
	err = feature.Activate("activator")
	require.NoError(t, err)

	// Terminate
	beforeTerminate := time.Now()
	updatedBy := "terminator"
	err = feature.Terminate(updatedBy)

	require.NoError(t, err)
	assert.Equal(t, ValuationFeatureLifecycleStatusTerminated, feature.LifecycleStatus)
	assert.Equal(t, updatedBy, feature.UpdatedBy)
	assert.True(t, feature.IsTerminal())
	// ValidTo should be set to current time (within reasonable tolerance)
	assert.True(t, feature.ValidTo.After(beforeTerminate))
	assert.True(t, feature.ValidTo.Before(time.Now().Add(1*time.Second)))
}

func TestValuationFeature_Terminate_Idempotent(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	// Activate and terminate
	err = feature.Activate("user1")
	require.NoError(t, err)
	err = feature.Terminate("user1")
	require.NoError(t, err)

	// Second termination - should be idempotent
	err = feature.Terminate("user2")
	require.NoError(t, err)
	assert.Equal(t, ValuationFeatureLifecycleStatusTerminated, feature.LifecycleStatus)
}

func TestValuationFeature_Terminate_InvalidTransition(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	// Try to terminate without activating first - should fail
	err = feature.Terminate("user1")
	require.Error(t, err)
	assert.Equal(t, ErrInvalidValuationFeatureTransition, err)
}

func TestValuationFeature_IsValidAt(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	// Set specific validity range for testing
	validFrom := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
	feature.ValidFrom = validFrom
	feature.ValidTo = validTo

	testCases := []struct {
		name        string
		knowledgeAt time.Time
		expected    bool
	}{
		{
			name:        "before validity range",
			knowledgeAt: time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC),
			expected:    false,
		},
		{
			name:        "at start of validity range",
			knowledgeAt: validFrom,
			expected:    true,
		},
		{
			name:        "within validity range",
			knowledgeAt: time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			expected:    true,
		},
		{
			name:        "at end of validity range",
			knowledgeAt: validTo,
			expected:    false, // ValidTo is exclusive
		},
		{
			name:        "after validity range",
			knowledgeAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			expected:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := feature.IsValidAt(tc.knowledgeAt)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestValuationFeature_LifecycleStates(t *testing.T) {
	feature, err := NewValuationFeature(uuid.New(), "USD", uuid.New(), 1, nil, "creator")
	require.NoError(t, err)

	// INITIATED state
	assert.False(t, feature.IsActive())
	assert.False(t, feature.IsTerminal())

	// ACTIVE state
	err = feature.Activate("user1")
	require.NoError(t, err)
	assert.True(t, feature.IsActive())
	assert.False(t, feature.IsTerminal())

	// TERMINATED state
	err = feature.Terminate("user1")
	require.NoError(t, err)
	assert.False(t, feature.IsActive())
	assert.True(t, feature.IsTerminal())
}
