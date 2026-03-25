package domain

import (
	"testing"

	"github.com/google/uuid"
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValuationFeature_LifecycleStatusConstants(t *testing.T) {
	assert.Equal(t, vf.LifecycleStatusInitiated, ValuationFeatureLifecycleStatusInitiated)
	assert.Equal(t, vf.LifecycleStatusActive, ValuationFeatureLifecycleStatusActive)
	assert.Equal(t, vf.LifecycleStatusTerminated, ValuationFeatureLifecycleStatusTerminated)
}

func TestValuationFeature_ErrorAliases(t *testing.T) {
	assert.Equal(t, vf.ErrInvalidLifecycleTransition, ErrInvalidValuationFeatureTransition)
	assert.Equal(t, vf.ErrNotActive, ErrValuationFeatureNotActive)
	assert.Equal(t, vf.ErrInvalidParameters, ErrInvalidValuationFeatureParameters)
	assert.Equal(t, vf.ErrInstrumentCodeEmpty, ErrValuationFeatureInstrumentEmpty)
}

func TestNewValuationFeature_DelegatesToShared(t *testing.T) {
	accountID := uuid.New()
	methodID := uuid.New()
	params := map[string]interface{}{"source": "ECB"}

	feature, err := NewValuationFeature(accountID, "USD", methodID, 1, params, "test-user")

	require.NoError(t, err)
	assert.NotNil(t, feature)
	assert.Equal(t, accountID, feature.AccountID)
	assert.Equal(t, "USD", feature.InstrumentCode)
	assert.Equal(t, methodID, feature.ValuationMethodID)
	assert.Equal(t, 1, feature.ValuationMethodVersion)
	assert.Equal(t, params, feature.Parameters)
	assert.Equal(t, ValuationFeatureLifecycleStatusInitiated, feature.LifecycleStatus)
	assert.Equal(t, "test-user", feature.CreatedBy)
	assert.Equal(t, 1, feature.Version)
}

func TestNewValuationFeature_EmptyInstrumentCode_ReturnsError(t *testing.T) {
	accountID := uuid.New()
	methodID := uuid.New()

	_, err := NewValuationFeature(accountID, "", methodID, 1, nil, "test-user")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValuationFeatureInstrumentEmpty)
}

func TestValuationFeature_TypeAlias_SupportsSharedMethods(t *testing.T) {
	accountID := uuid.New()
	methodID := uuid.New()

	feature, err := NewValuationFeature(accountID, "GBP", methodID, 2, nil, "creator")
	require.NoError(t, err)

	assert.False(t, feature.IsActive())
	assert.False(t, feature.IsTerminal())

	// Activate transitions to ACTIVE.
	err = feature.Activate("activator")
	require.NoError(t, err)
	assert.True(t, feature.IsActive())
	assert.Equal(t, ValuationFeatureLifecycleStatusActive, feature.LifecycleStatus)

	// Terminate transitions to TERMINATED.
	err = feature.Terminate("terminator")
	require.NoError(t, err)
	assert.True(t, feature.IsTerminal())
	assert.Equal(t, ValuationFeatureLifecycleStatusTerminated, feature.LifecycleStatus)
}

func TestValuationFeature_LifecycleStatus_IsString(t *testing.T) {
	assert.Equal(t, ValuationFeatureLifecycleStatus("INITIATED"), ValuationFeatureLifecycleStatusInitiated)
	assert.Equal(t, ValuationFeatureLifecycleStatus("ACTIVE"), ValuationFeatureLifecycleStatusActive)
	assert.Equal(t, ValuationFeatureLifecycleStatus("TERMINATED"), ValuationFeatureLifecycleStatusTerminated)
}
