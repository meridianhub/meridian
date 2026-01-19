package service

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func setupTestServerForDataSet(t *testing.T) (*Server, *testhelpers.TestContainer, func()) {
	t.Helper()

	tc := testhelpers.SetupTestContainer(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	server, err := NewServer(
		tc.Repos.DataSet,
		tc.Repos.Observation,
		tc.Repos.Source,
		WithLogger(logger),
	)
	require.NoError(t, err)

	cleanup := func() {
		tc.Cleanup(t)
	}

	return server, tc, cleanup
}

func TestRegisterDataSet_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("successfully registers new dataset with all fields", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "TEST_FX_RATE",
			DisplayName:             "Test FX Rate Dataset",
			Description:             "Test description for FX rates",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "value > 0",
			ResolutionKeyExpression: "currency_pair",
			ErrorMessageExpression:  "'Value must be positive'",
		}

		resp, err := server.RegisterDataSet(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Dataset)

		assert.Equal(t, "TEST_FX_RATE", resp.Dataset.Code)
		assert.Equal(t, "Test FX Rate Dataset", resp.Dataset.DisplayName)
		assert.Equal(t, "Test description for FX rates", resp.Dataset.Description)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_DRAFT, resp.Dataset.Status)
		assert.Equal(t, int32(1), resp.Dataset.Version)
		assert.NotEmpty(t, resp.Dataset.Id)
		assert.NotNil(t, resp.Dataset.CreatedAt)
	})

	t.Run("successfully registers dataset with minimal fields", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "MINIMAL_DATASET",
			DisplayName:             "Minimal Dataset",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		resp, err := server.RegisterDataSet(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "MINIMAL_DATASET", resp.Dataset.Code)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_DRAFT, resp.Dataset.Status)
	})
}

func TestRegisterDataSet_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns ALREADY_EXISTS for duplicate code", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "DUPLICATE_TEST",
			DisplayName:             "Duplicate Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		// First registration should succeed
		_, err := server.RegisterDataSet(ctx, req)
		require.NoError(t, err)

		// Second registration should fail
		_, err = server.RegisterDataSet(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
		assert.Contains(t, st.Message(), "DUPLICATE_TEST")
	})

	t.Run("returns INVALID_ARGUMENT for invalid category", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "INVALID_CATEGORY",
			DisplayName:             "Invalid Category Test",
			Category:                pb.DataCategory_DATA_CATEGORY_UNSPECIFIED,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		_, err := server.RegisterDataSet(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "category")
	})

	t.Run("returns INVALID_ARGUMENT for empty code", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "",
			DisplayName:             "Empty Code Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		_, err := server.RegisterDataSet(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns INVALID_ARGUMENT for empty display name", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "NO_NAME",
			DisplayName:             "",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		_, err := server.RegisterDataSet(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestUpdateDataSet_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("successfully updates dataset description", func(t *testing.T) {
		// First, register a dataset
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "UPDATE_TEST_DESC",
			DisplayName:             "Update Test",
			Description:             "Original description",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		// Update the description
		updateReq := &pb.UpdateDataSetRequest{
			Code:        "UPDATE_TEST_DESC",
			Version:     registerResp.Dataset.Version,
			Description: "Updated description",
		}

		updateResp, err := server.UpdateDataSet(ctx, updateReq)
		require.NoError(t, err)
		require.NotNil(t, updateResp)
		assert.Equal(t, "Updated description", updateResp.Dataset.Description)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_DRAFT, updateResp.Dataset.Status)
	})

	t.Run("successfully updates validation expression", func(t *testing.T) {
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "UPDATE_TEST_VALIDATION",
			DisplayName:             "Update Validation Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "value > 0",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		updateReq := &pb.UpdateDataSetRequest{
			Code:                 "UPDATE_TEST_VALIDATION",
			Version:              registerResp.Dataset.Version,
			ValidationExpression: "value > 10",
		}

		updateResp, err := server.UpdateDataSet(ctx, updateReq)
		require.NoError(t, err)
		assert.Equal(t, "value > 10", updateResp.Dataset.ValidationExpression)
	})
}

func TestUpdateDataSet_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns NOT_FOUND for non-existent dataset", func(t *testing.T) {
		updateReq := &pb.UpdateDataSetRequest{
			Code:        "NONEXISTENT",
			Version:     1,
			Description: "New description",
		}

		_, err := server.UpdateDataSet(ctx, updateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns FAILED_PRECONDITION for non-draft dataset", func(t *testing.T) {
		// Register and activate a dataset
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "ACTIVE_DATASET",
			DisplayName:             "Active Dataset",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		// Activate it
		activateReq := &pb.ActivateDataSetRequest{
			Code:    "ACTIVE_DATASET",
			Version: registerResp.Dataset.Version,
		}
		activateResp, err := server.ActivateDataSet(ctx, activateReq)
		require.NoError(t, err)

		// Try to update - should fail (use version from activate response)
		updateReq := &pb.UpdateDataSetRequest{
			Code:        "ACTIVE_DATASET",
			Version:     activateResp.Dataset.Version,
			Description: "This should fail",
		}

		_, err = server.UpdateDataSet(ctx, updateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
		assert.Contains(t, st.Message(), "DRAFT")
	})
}

func TestActivateDataSet_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("successfully activates draft dataset", func(t *testing.T) {
		// Register a dataset
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "ACTIVATE_TEST",
			DisplayName:             "Activate Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_DRAFT, registerResp.Dataset.Status)

		// Activate it
		activateReq := &pb.ActivateDataSetRequest{
			Code:    "ACTIVATE_TEST",
			Version: registerResp.Dataset.Version,
		}

		activateResp, err := server.ActivateDataSet(ctx, activateReq)
		require.NoError(t, err)
		require.NotNil(t, activateResp)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_ACTIVE, activateResp.Dataset.Status)
		assert.Equal(t, "ACTIVATE_TEST", activateResp.Dataset.Code)
	})
}

func TestActivateDataSet_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns NOT_FOUND for non-existent dataset", func(t *testing.T) {
		activateReq := &pb.ActivateDataSetRequest{
			Code:    "NONEXISTENT",
			Version: 1,
		}

		_, err := server.ActivateDataSet(ctx, activateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns FAILED_PRECONDITION when already active", func(t *testing.T) {
		// Register and activate
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "ALREADY_ACTIVE",
			DisplayName:             "Already Active",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		activateReq := &pb.ActivateDataSetRequest{
			Code:    "ALREADY_ACTIVE",
			Version: registerResp.Dataset.Version,
		}

		activateResp, err := server.ActivateDataSet(ctx, activateReq)
		require.NoError(t, err)

		// Try to activate again - should fail (use version from activate response)
		activateReq2 := &pb.ActivateDataSetRequest{
			Code:    "ALREADY_ACTIVE",
			Version: activateResp.Dataset.Version,
		}
		_, err = server.ActivateDataSet(ctx, activateReq2)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestDeprecateDataSet_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("successfully deprecates active dataset", func(t *testing.T) {
		// Register and activate
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "DEPRECATE_TEST",
			DisplayName:             "Deprecate Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		activateReq := &pb.ActivateDataSetRequest{
			Code:    "DEPRECATE_TEST",
			Version: registerResp.Dataset.Version,
		}

		activateResp, err := server.ActivateDataSet(ctx, activateReq)
		require.NoError(t, err)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_ACTIVE, activateResp.Dataset.Status)

		// Deprecate it
		deprecateReq := &pb.DeprecateDataSetRequest{
			Code:    "DEPRECATE_TEST",
			Version: activateResp.Dataset.Version,
		}

		deprecateResp, err := server.DeprecateDataSet(ctx, deprecateReq)
		require.NoError(t, err)
		require.NotNil(t, deprecateResp)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_DEPRECATED, deprecateResp.Dataset.Status)
	})
}

func TestDeprecateDataSet_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns NOT_FOUND for non-existent dataset", func(t *testing.T) {
		deprecateReq := &pb.DeprecateDataSetRequest{
			Code:    "NONEXISTENT",
			Version: 1,
		}

		_, err := server.DeprecateDataSet(ctx, deprecateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestRetrieveDataSet_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("retrieves dataset by code and specific version", func(t *testing.T) {
		// Register a dataset
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "RETRIEVE_TEST",
			DisplayName:             "Retrieve Test",
			Description:             "Test retrieval",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		// Retrieve it
		retrieveReq := &pb.RetrieveDataSetRequest{
			Code:    "RETRIEVE_TEST",
			Version: registerResp.Dataset.Version,
		}

		retrieveResp, err := server.RetrieveDataSet(ctx, retrieveReq)
		require.NoError(t, err)
		require.NotNil(t, retrieveResp)
		assert.Equal(t, "RETRIEVE_TEST", retrieveResp.Dataset.Code)
		assert.Equal(t, "Retrieve Test", retrieveResp.Dataset.DisplayName)
		assert.Equal(t, "Test retrieval", retrieveResp.Dataset.Description)
	})

	t.Run("retrieves latest version when version is 0", func(t *testing.T) {
		// Register a dataset
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "LATEST_VERSION_TEST",
			DisplayName:             "Latest Version Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		_, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		// Retrieve with version 0 (latest)
		retrieveReq := &pb.RetrieveDataSetRequest{
			Code:    "LATEST_VERSION_TEST",
			Version: 0,
		}

		retrieveResp, err := server.RetrieveDataSet(ctx, retrieveReq)
		require.NoError(t, err)
		require.NotNil(t, retrieveResp)
		assert.Equal(t, "LATEST_VERSION_TEST", retrieveResp.Dataset.Code)
		assert.Equal(t, int32(1), retrieveResp.Dataset.Version)
	})
}

func TestRetrieveDataSet_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns NOT_FOUND for non-existent dataset", func(t *testing.T) {
		retrieveReq := &pb.RetrieveDataSetRequest{
			Code:    "NONEXISTENT",
			Version: 1,
		}

		_, err := server.RetrieveDataSet(ctx, retrieveReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestListDataSets_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("lists all datasets without filters", func(t *testing.T) {
		// Register multiple datasets
		for i := 1; i <= 3; i++ {
			code := "LIST_TEST_"
			switch i {
			case 1:
				code += "A"
			case 2:
				code += "B"
			case 3:
				code += "C"
			}
			req := &pb.RegisterDataSetRequest{
				Code:                    code,
				DisplayName:             "List Test Dataset",
				Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
				ValidationExpression:    "true",
				ResolutionKeyExpression: "key",
			}
			_, err := server.RegisterDataSet(ctx, req)
			require.NoError(t, err)
		}

		// List all
		listReq := &pb.ListDataSetsRequest{
			PageSize: 10,
		}

		listResp, err := server.ListDataSets(ctx, listReq)
		require.NoError(t, err)
		require.NotNil(t, listResp)
		assert.GreaterOrEqual(t, len(listResp.Datasets), 3)
	})

	t.Run("filters datasets by status", func(t *testing.T) {
		// Register and activate one dataset
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "FILTER_ACTIVE",
			DisplayName:             "Filter Active Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		activateReq := &pb.ActivateDataSetRequest{
			Code:    "FILTER_ACTIVE",
			Version: registerResp.Dataset.Version,
		}
		_, err = server.ActivateDataSet(ctx, activateReq)
		require.NoError(t, err)

		// List only ACTIVE datasets
		listReq := &pb.ListDataSetsRequest{
			StatusFilter: pb.DataSetStatus_DATA_SET_STATUS_ACTIVE,
			PageSize:     100,
		}

		listResp, err := server.ListDataSets(ctx, listReq)
		require.NoError(t, err)

		// Verify all returned datasets are ACTIVE
		for _, ds := range listResp.Datasets {
			assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_ACTIVE, ds.Status)
		}
	})

	t.Run("filters datasets by category", func(t *testing.T) {
		// Register dataset with specific category
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "FILTER_CATEGORY",
			DisplayName:             "Filter Category Test",
			Category:                pb.DataCategory_DATA_CATEGORY_INTEREST_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		_, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		// List with category filter
		listReq := &pb.ListDataSetsRequest{
			CategoryFilter: pb.DataCategory_DATA_CATEGORY_INTEREST_RATE,
			PageSize:       100,
		}

		listResp, err := server.ListDataSets(ctx, listReq)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(listResp.Datasets), 1)
	})
}

func TestDataSetStatusTransitions(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	tests := []struct {
		name          string
		setupStatus   domain.DataSetStatus
		operation     string
		expectSuccess bool
		expectedCode  codes.Code
	}{
		{
			name:          "DRAFT to ACTIVE allowed",
			setupStatus:   domain.DataSetStatusDraft,
			operation:     "activate",
			expectSuccess: true,
		},
		{
			name:          "ACTIVE to DEPRECATED allowed",
			setupStatus:   domain.DataSetStatusActive,
			operation:     "deprecate",
			expectSuccess: true,
		},
		{
			name:          "ACTIVE to ACTIVE not allowed",
			setupStatus:   domain.DataSetStatusActive,
			operation:     "activate",
			expectSuccess: false,
			expectedCode:  codes.FailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Register dataset with slugified name (replace spaces with underscores)
			code := "TRANSITION_" + strings.ReplaceAll(strings.ToUpper(tt.name), " ", "_")
			registerReq := &pb.RegisterDataSetRequest{
				Code:                    code,
				DisplayName:             "Transition Test",
				Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
				ValidationExpression:    "true",
				ResolutionKeyExpression: "key",
			}

			registerResp, err := server.RegisterDataSet(ctx, registerReq)
			require.NoError(t, err)
			version := registerResp.Dataset.Version

			// Setup to desired initial status
			if tt.setupStatus == domain.DataSetStatusActive {
				activateReq := &pb.ActivateDataSetRequest{
					Code:    code,
					Version: version,
				}
				activateResp, err := server.ActivateDataSet(ctx, activateReq)
				require.NoError(t, err)
				version = activateResp.Dataset.Version
			}

			// Perform operation
			var opErr error
			switch tt.operation {
			case "activate":
				activateReq := &pb.ActivateDataSetRequest{
					Code:    code,
					Version: version,
				}
				_, opErr = server.ActivateDataSet(ctx, activateReq)
			case "deprecate":
				deprecateReq := &pb.DeprecateDataSetRequest{
					Code:    code,
					Version: version,
				}
				_, opErr = server.DeprecateDataSet(ctx, deprecateReq)
			}

			// Verify result
			if tt.expectSuccess {
				assert.NoError(t, opErr)
			} else {
				require.Error(t, opErr)
				st, ok := status.FromError(opErr)
				require.True(t, ok)
				assert.Equal(t, tt.expectedCode, st.Code())
			}
		})
	}
}
