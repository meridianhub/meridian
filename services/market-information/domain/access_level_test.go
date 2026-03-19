package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataAccessLevel_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		level    DataAccessLevel
		expected bool
	}{
		{"PUBLIC is valid", AccessLevelPublic, true},
		{"PRIVATE is valid", AccessLevelPrivate, true},
		{"RESTRICTED is valid", AccessLevelRestricted, true},
		{"empty is invalid", DataAccessLevel(""), false},
		{"lowercase is invalid", DataAccessLevel("public"), false},
		{"unknown is invalid", DataAccessLevel("UNKNOWN"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.IsValid())
		})
	}
}

func TestDataAccessLevel_String(t *testing.T) {
	assert.Equal(t, "PUBLIC", AccessLevelPublic.String())
	assert.Equal(t, "PRIVATE", AccessLevelPrivate.String())
	assert.Equal(t, "RESTRICTED", AccessLevelRestricted.String())
}

func TestDataSetDefinition_IsShared(t *testing.T) {
	t.Run("default is not shared", func(t *testing.T) {
		ds := createValidDataSetDefinition(t)
		assert.False(t, ds.IsShared())
	})

	t.Run("builder can set shared", func(t *testing.T) {
		ds := NewDataSetDefinitionBuilder().
			WithIsShared(true).
			Build()
		assert.True(t, ds.IsShared())
	})

	t.Run("builder can set not shared", func(t *testing.T) {
		ds := NewDataSetDefinitionBuilder().
			WithIsShared(false).
			Build()
		assert.False(t, ds.IsShared())
	})
}

func TestDataSetDefinition_AccessLevel(t *testing.T) {
	t.Run("default is PRIVATE", func(t *testing.T) {
		ds := createValidDataSetDefinition(t)
		assert.Equal(t, AccessLevelPrivate, ds.AccessLevel())
	})

	t.Run("builder can set PUBLIC", func(t *testing.T) {
		ds := NewDataSetDefinitionBuilder().
			WithAccessLevel(AccessLevelPublic).
			Build()
		assert.Equal(t, AccessLevelPublic, ds.AccessLevel())
	})

	t.Run("builder can set RESTRICTED", func(t *testing.T) {
		ds := NewDataSetDefinitionBuilder().
			WithAccessLevel(AccessLevelRestricted).
			Build()
		assert.Equal(t, AccessLevelRestricted, ds.AccessLevel())
	})
}

func TestDataSetDefinition_WithIsShared(t *testing.T) {
	ds := NewDataSetDefinitionBuilder().
		WithIsShared(true).
		WithAccessLevel(AccessLevelPublic).
		Build()

	assert.True(t, ds.IsShared())
	assert.Equal(t, AccessLevelPublic, ds.AccessLevel())
}

func TestDataSetDefinition_WithAccessLevel(t *testing.T) {
	levels := []DataAccessLevel{AccessLevelPublic, AccessLevelPrivate, AccessLevelRestricted}
	for _, level := range levels {
		t.Run(level.String(), func(t *testing.T) {
			ds := NewDataSetDefinitionBuilder().
				WithAccessLevel(level).
				Build()
			assert.Equal(t, level, ds.AccessLevel())
		})
	}
}
