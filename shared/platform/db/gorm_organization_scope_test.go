package db

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestWithGormOrganizationScope_SetsSearchPath(t *testing.T) {
	// Create mock database
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Setup context with organization
	orgID := organization.OrganizationID("acme_bank")
	ctx := organization.WithOrganization(context.Background(), orgID)

	// Expect the SET LOCAL query
	mock.ExpectExec(`SET LOCAL search_path TO "org_acme_bank", public`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Execute
	result, err := WithGormOrganizationScope(ctx, gormDB)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithGormOrganizationScope_MissingContext_ReturnsError(t *testing.T) {
	// Create mock database
	mockDB, _, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Context without organization
	ctx := context.Background()

	// Execute
	result, err := WithGormOrganizationScope(ctx, gormDB)

	// Assert
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, organization.ErrMissingOrganizationContext)
}

func TestMustWithGormOrganizationScope_MissingContext_Panics(t *testing.T) {
	// Create mock database
	mockDB, _, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Create GORM instance with mock
	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		Conn: mockDB,
	}), &gorm.Config{})
	require.NoError(t, err)

	// Context without organization
	ctx := context.Background()

	// Assert panic
	assert.Panics(t, func() {
		MustWithGormOrganizationScope(ctx, gormDB)
	})
}

func TestWithGormOrganizationScope_SpecialCharacters_QuotedProperly(t *testing.T) {
	testCases := []struct {
		name           string
		orgID          string
		expectedSchema string
	}{
		{
			name:           "simple org id",
			orgID:          "acme",
			expectedSchema: `"org_acme"`,
		},
		{
			name:           "org id with underscore",
			orgID:          "acme_bank",
			expectedSchema: `"org_acme_bank"`,
		},
		{
			name:           "org id with numbers",
			orgID:          "bank123",
			expectedSchema: `"org_bank123"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock database
			mockDB, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer mockDB.Close()

			// Create GORM instance with mock
			gormDB, err := gorm.Open(postgres.New(postgres.Config{
				Conn: mockDB,
			}), &gorm.Config{})
			require.NoError(t, err)

			// Setup context
			orgID := organization.OrganizationID(tc.orgID)
			ctx := organization.WithOrganization(context.Background(), orgID)

			// Expect the SET LOCAL query with properly quoted schema
			expected := "SET LOCAL search_path TO " + tc.expectedSchema + ", public"
			mock.ExpectExec(expected).
				WillReturnResult(sqlmock.NewResult(0, 0))

			// Execute
			result, err := WithGormOrganizationScope(ctx, gormDB)

			// Assert
			require.NoError(t, err)
			assert.NotNil(t, result)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
