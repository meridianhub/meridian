package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceRepository_Delete(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("soft-deletes existing source", func(t *testing.T) {
		source, err := domain.NewDataSource("DELETE_ME", "Delete Me", "To be deleted", domain.SourceTypeAPI, 50)
		require.NoError(t, err)
		err = tc.Repos.Source.Save(ctx, source)
		require.NoError(t, err)

		// Delete
		err = tc.Repos.Source.Delete(ctx, "DELETE_ME")
		require.NoError(t, err)

		// Should not be findable
		_, err = tc.Repos.Source.FindByCode(ctx, "DELETE_ME")
		assert.ErrorIs(t, err, domain.ErrDataSourceNotFound)
	})

	t.Run("returns not found for non-existent source", func(t *testing.T) {
		err := tc.Repos.Source.Delete(ctx, "NONEXISTENT_CODE")
		assert.ErrorIs(t, err, domain.ErrDataSourceNotFound)
	})

	t.Run("returns not found for already deleted source", func(t *testing.T) {
		source, err := domain.NewDataSource("DELETE_TWICE", "Delete Twice", "", domain.SourceTypeAPI, 50)
		require.NoError(t, err)
		err = tc.Repos.Source.Save(ctx, source)
		require.NoError(t, err)

		err = tc.Repos.Source.Delete(ctx, "DELETE_TWICE")
		require.NoError(t, err)

		err = tc.Repos.Source.Delete(ctx, "DELETE_TWICE")
		assert.ErrorIs(t, err, domain.ErrDataSourceNotFound)
	})

	t.Run("deleted source excluded from list", func(t *testing.T) {
		source, err := domain.NewDataSource("DELETE_LIST_TEST", "Delete List", "", domain.SourceTypeAPI, 50)
		require.NoError(t, err)
		err = tc.Repos.Source.Save(ctx, source)
		require.NoError(t, err)

		err = tc.Repos.Source.Delete(ctx, "DELETE_LIST_TEST")
		require.NoError(t, err)

		sources, _, err := tc.Repos.Source.List(ctx, false, 100, "")
		require.NoError(t, err)
		for _, s := range sources {
			assert.NotEqual(t, "DELETE_LIST_TEST", s.Code())
		}
	})
}

func TestSourceRepository_GetTrustLevel(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	// Cast to concrete type to access GetTrustLevel (not on domain interface)
	sourceRepo := tc.Repos.Source.(*persistence.SourceRepository)

	ctx := context.Background()

	t.Run("returns trust level for existing source", func(t *testing.T) {
		source, err := domain.NewDataSource("TRUST_LEVEL_TEST", "Trust Level Test", "", domain.SourceTypeAPI, 85)
		require.NoError(t, err)
		err = tc.Repos.Source.Save(ctx, source)
		require.NoError(t, err)

		// Get the source ID
		found, err := tc.Repos.Source.FindByCode(ctx, "TRUST_LEVEL_TEST")
		require.NoError(t, err)

		trustLevel, err := sourceRepo.GetTrustLevel(ctx, found.ID())
		require.NoError(t, err)
		assert.Equal(t, 85, trustLevel)
	})

	t.Run("returns not found for non-existent source", func(t *testing.T) {
		_, err := sourceRepo.GetTrustLevel(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrDataSourceNotFound)
	})

	t.Run("returns not found for deleted source", func(t *testing.T) {
		source, err := domain.NewDataSource("TRUST_DELETED", "Trust Deleted", "", domain.SourceTypeAPI, 50)
		require.NoError(t, err)
		err = tc.Repos.Source.Save(ctx, source)
		require.NoError(t, err)

		found, err := tc.Repos.Source.FindByCode(ctx, "TRUST_DELETED")
		require.NoError(t, err)

		err = tc.Repos.Source.Delete(ctx, "TRUST_DELETED")
		require.NoError(t, err)

		_, err = sourceRepo.GetTrustLevel(ctx, found.ID())
		assert.ErrorIs(t, err, domain.ErrDataSourceNotFound)
	})
}

func TestSourceRepository_List_Pagination(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Create enough sources for pagination testing
	for i := 0; i < 5; i++ {
		code := "PAGE_SRC_" + string(rune('A'+i))
		source, err := domain.NewDataSource(code, "Source "+code, "", domain.SourceTypeAPI, 50+i)
		require.NoError(t, err)
		err = tc.Repos.Source.Save(ctx, source)
		require.NoError(t, err)
	}

	t.Run("paginates with small page size", func(t *testing.T) {
		// First page
		page1, token1, err := tc.Repos.Source.List(ctx, false, 2, "")
		require.NoError(t, err)
		assert.Len(t, page1, 2)
		assert.NotEmpty(t, token1)

		// Second page
		page2, token2, err := tc.Repos.Source.List(ctx, false, 2, token1)
		require.NoError(t, err)
		assert.Len(t, page2, 2)
		assert.NotEmpty(t, token2)

		// Third page
		page3, token3, err := tc.Repos.Source.List(ctx, false, 2, token2)
		require.NoError(t, err)
		assert.Len(t, page3, 1)
		assert.Empty(t, token3) // No more pages
	})

	t.Run("rejects invalid page token", func(t *testing.T) {
		_, _, err := tc.Repos.Source.List(ctx, false, 10, "invalid-token!!!")
		assert.ErrorIs(t, err, domain.ErrInvalidPageToken)
	})
}
