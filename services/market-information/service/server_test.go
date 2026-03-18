package service

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer_NilDependencies(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	t.Run("returns error for nil dataset repository", func(t *testing.T) {
		_, err := NewServer(nil, tc.Repos.Observation, tc.Repos.Source)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDataSetRepositoryNil)
	})

	t.Run("returns error for nil observation repository", func(t *testing.T) {
		_, err := NewServer(tc.Repos.DataSet, nil, tc.Repos.Source)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrObservationRepositoryNil)
	})

	t.Run("returns error for nil source repository", func(t *testing.T) {
		_, err := NewServer(tc.Repos.DataSet, tc.Repos.Observation, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSourceRepositoryNil)
	})
}

func TestNewServer_WithOptions(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	t.Run("applies WithCelValidator option", func(t *testing.T) {
		validator, err := NewCelValidator()
		require.NoError(t, err)

		server, err := NewServer(
			tc.Repos.DataSet,
			tc.Repos.Observation,
			tc.Repos.Source,
			WithCelValidator(validator),
		)
		require.NoError(t, err)
		assert.NotNil(t, server.celValidator)
	})

	t.Run("applies WithEventPublisher option", func(t *testing.T) {
		pub := &mockEventPublisher{}

		server, err := NewServer(
			tc.Repos.DataSet,
			tc.Repos.Observation,
			tc.Repos.Source,
			WithEventPublisher(pub),
		)
		require.NoError(t, err)
		assert.NotNil(t, server.eventPublisher)
	})

	t.Run("applies WithLogger option", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

		server, err := NewServer(
			tc.Repos.DataSet,
			tc.Repos.Observation,
			tc.Repos.Source,
			WithLogger(logger),
		)
		require.NoError(t, err)
		assert.Equal(t, logger, server.logger)
	})

	t.Run("defaults logger when not provided", func(t *testing.T) {
		server, err := NewServer(
			tc.Repos.DataSet,
			tc.Repos.Observation,
			tc.Repos.Source,
		)
		require.NoError(t, err)
		assert.NotNil(t, server.logger)
	})
}

// mockEventPublisher implements EventPublisher for testing.
type mockEventPublisher struct {
	publishedEvents []any
}

func (m *mockEventPublisher) Publish(_ context.Context, event any) error {
	m.publishedEvents = append(m.publishedEvents, event)
	return nil
}
