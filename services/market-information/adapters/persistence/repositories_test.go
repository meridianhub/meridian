package persistence_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRepositories_PanicsOnEmptyMasterTenantID(t *testing.T) {
	assert.Panics(t, func() {
		// nil pool is fine here - panics before using it
		persistence.NewRepositories(nil, "")
	})
}

func TestNewRepositories_ReturnsNonNilRepositories(t *testing.T) {
	// NewRepositories stores the pool but does not connect during construction.
	// Passing nil is sufficient for verifying the returned struct is populated.
	repos := persistence.NewRepositories(nil, "master_tenant")
	require.NotNil(t, repos)
	assert.NotNil(t, repos.DataSet)
	assert.NotNil(t, repos.Observation)
	assert.NotNil(t, repos.Source)
}

func TestNewRepositories_AllFieldsPopulated(t *testing.T) {
	repos := persistence.NewRepositories(nil, "any_master_id")

	require.NotNil(t, repos.DataSet, "DataSet repository should not be nil")
	require.NotNil(t, repos.Observation, "Observation repository should not be nil")
	require.NotNil(t, repos.Source, "Source repository should not be nil")
}
