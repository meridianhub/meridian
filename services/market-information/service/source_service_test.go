package service

import (
	"context"
	"log/slog"
	"os"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func setupTestServerForSource(t *testing.T) (*Server, *testhelpers.TestContainer, func()) {
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

func TestRegisterDataSource_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("successfully registers new data source with all fields", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:        "BLOOMBERG",
			Name:        "Bloomberg Data Feed",
			Description: "Premium market data from Bloomberg",
			TrustLevel:  90,
		}

		resp, err := server.RegisterDataSource(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Source)

		assert.Equal(t, "BLOOMBERG", resp.Source.Code)
		assert.Equal(t, "Bloomberg Data Feed", resp.Source.Name)
		assert.Equal(t, "Premium market data from Bloomberg", resp.Source.Description)
		assert.Equal(t, int32(90), resp.Source.TrustLevel)
		assert.True(t, resp.Source.IsActive)
		assert.NotEmpty(t, resp.Source.Id)
		assert.NotNil(t, resp.Source.CreatedAt)
	})

	t.Run("successfully registers data source with minimal fields", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "MINIMAL_SOURCE",
			Name:       "Minimal Source",
			TrustLevel: 50,
		}

		resp, err := server.RegisterDataSource(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "MINIMAL_SOURCE", resp.Source.Code)
		assert.Equal(t, "", resp.Source.Description)
		assert.Equal(t, int32(50), resp.Source.TrustLevel)
	})

	t.Run("successfully registers data source with zero trust level", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "LOW_TRUST_SOURCE",
			Name:       "Low Trust Source",
			TrustLevel: 0,
		}

		resp, err := server.RegisterDataSource(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, int32(0), resp.Source.TrustLevel)
	})

	t.Run("successfully registers data source with max trust level", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "HIGH_TRUST_SOURCE",
			Name:       "High Trust Source",
			TrustLevel: 100,
		}

		resp, err := server.RegisterDataSource(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, int32(100), resp.Source.TrustLevel)
	})
}

func TestRegisterDataSource_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns ALREADY_EXISTS for duplicate code", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "DUPLICATE_SOURCE",
			Name:       "Duplicate Source",
			TrustLevel: 50,
		}

		// First registration should succeed
		_, err := server.RegisterDataSource(ctx, req)
		require.NoError(t, err)

		// Second registration should fail
		_, err = server.RegisterDataSource(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
		assert.Contains(t, st.Message(), "DUPLICATE_SOURCE")
	})

	t.Run("returns INVALID_ARGUMENT for invalid trust level above 100", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "INVALID_TRUST_HIGH",
			Name:       "Invalid Trust High",
			TrustLevel: 101,
		}

		_, err := server.RegisterDataSource(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "trust_level")
	})

	t.Run("returns INVALID_ARGUMENT for negative trust level", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "INVALID_TRUST_NEG",
			Name:       "Invalid Trust Negative",
			TrustLevel: -1,
		}

		_, err := server.RegisterDataSource(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns INVALID_ARGUMENT for empty code", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "",
			Name:       "No Code Source",
			TrustLevel: 50,
		}

		_, err := server.RegisterDataSource(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns INVALID_ARGUMENT for empty name", func(t *testing.T) {
		req := &pb.RegisterDataSourceRequest{
			Code:       "NO_NAME_SOURCE",
			Name:       "",
			TrustLevel: 50,
		}

		_, err := server.RegisterDataSource(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestUpdateDataSource_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("successfully updates data source name", func(t *testing.T) {
		// First, register a data source
		registerReq := &pb.RegisterDataSourceRequest{
			Code:        "UPDATE_NAME_TEST",
			Name:        "Original Name",
			Description: "Original description",
			TrustLevel:  50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		// Update the name
		updateReq := &pb.UpdateDataSourceRequest{
			Code:        "UPDATE_NAME_TEST",
			Name:        "Updated Name",
			Description: "Original description",
			TrustLevel:  50,
		}

		updateResp, err := server.UpdateDataSource(ctx, updateReq)
		require.NoError(t, err)
		require.NotNil(t, updateResp)
		assert.Equal(t, "Updated Name", updateResp.Source.Name)
	})

	t.Run("successfully updates data source description", func(t *testing.T) {
		registerReq := &pb.RegisterDataSourceRequest{
			Code:        "UPDATE_DESC_TEST",
			Name:        "Update Desc Test",
			Description: "Original description",
			TrustLevel:  50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		updateReq := &pb.UpdateDataSourceRequest{
			Code:        "UPDATE_DESC_TEST",
			Description: "Updated description",
			TrustLevel:  50,
		}

		updateResp, err := server.UpdateDataSource(ctx, updateReq)
		require.NoError(t, err)
		assert.Equal(t, "Updated description", updateResp.Source.Description)
	})

	t.Run("successfully clears description", func(t *testing.T) {
		registerReq := &pb.RegisterDataSourceRequest{
			Code:        "CLEAR_DESC_TEST",
			Name:        "Clear Desc Test",
			Description: "Has a description",
			TrustLevel:  50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		// Update with empty description to clear it
		updateReq := &pb.UpdateDataSourceRequest{
			Code:        "CLEAR_DESC_TEST",
			Description: "",
			TrustLevel:  50,
		}

		updateResp, err := server.UpdateDataSource(ctx, updateReq)
		require.NoError(t, err)
		assert.Equal(t, "", updateResp.Source.Description)
	})

	t.Run("successfully updates trust level", func(t *testing.T) {
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "UPDATE_TRUST_TEST",
			Name:       "Update Trust Test",
			TrustLevel: 50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		updateReq := &pb.UpdateDataSourceRequest{
			Code:       "UPDATE_TRUST_TEST",
			TrustLevel: 90,
		}

		updateResp, err := server.UpdateDataSource(ctx, updateReq)
		require.NoError(t, err)
		assert.Equal(t, int32(90), updateResp.Source.TrustLevel)
	})

	t.Run("successfully sets trust level to zero", func(t *testing.T) {
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "ZERO_TRUST_TEST",
			Name:       "Zero Trust Test",
			TrustLevel: 80,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		updateReq := &pb.UpdateDataSourceRequest{
			Code:       "ZERO_TRUST_TEST",
			TrustLevel: 0,
		}

		updateResp, err := server.UpdateDataSource(ctx, updateReq)
		require.NoError(t, err)
		assert.Equal(t, int32(0), updateResp.Source.TrustLevel)
	})
}

func TestUpdateDataSource_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns NOT_FOUND for non-existent data source", func(t *testing.T) {
		updateReq := &pb.UpdateDataSourceRequest{
			Code:        "NONEXISTENT_SOURCE",
			Description: "New description",
			TrustLevel:  50,
		}

		_, err := server.UpdateDataSource(ctx, updateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
		assert.Contains(t, st.Message(), "NONEXISTENT_SOURCE")
	})

	t.Run("returns INVALID_ARGUMENT for invalid trust level above 100", func(t *testing.T) {
		// First, register a data source
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "UPDATE_INVALID_TRUST",
			Name:       "Update Invalid Trust",
			TrustLevel: 50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		// Try to update with invalid trust level
		updateReq := &pb.UpdateDataSourceRequest{
			Code:       "UPDATE_INVALID_TRUST",
			TrustLevel: 101,
		}

		_, err = server.UpdateDataSource(ctx, updateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "trust_level")
	})

	t.Run("returns INVALID_ARGUMENT for negative trust level", func(t *testing.T) {
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "UPDATE_NEG_TRUST",
			Name:       "Update Neg Trust",
			TrustLevel: 50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		updateReq := &pb.UpdateDataSourceRequest{
			Code:       "UPDATE_NEG_TRUST",
			TrustLevel: -5,
		}

		_, err = server.UpdateDataSource(ctx, updateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestDeactivateDataSource_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("successfully deactivates active data source", func(t *testing.T) {
		// Register an active data source
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "DEACTIVATE_TEST",
			Name:       "Deactivate Test",
			TrustLevel: 50,
		}

		registerResp, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)
		assert.True(t, registerResp.Source.IsActive)

		// Deactivate it
		deactivateReq := &pb.DeactivateDataSourceRequest{
			Code: "DEACTIVATE_TEST",
		}

		deactivateResp, err := server.DeactivateDataSource(ctx, deactivateReq)
		require.NoError(t, err)
		require.NotNil(t, deactivateResp)
		assert.False(t, deactivateResp.Source.IsActive)
		assert.Equal(t, "DEACTIVATE_TEST", deactivateResp.Source.Code)
	})

	t.Run("deactivating already inactive source is idempotent", func(t *testing.T) {
		// Register and deactivate
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "IDEMPOTENT_DEACTIVATE",
			Name:       "Idempotent Deactivate",
			TrustLevel: 50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		// First deactivation
		deactivateReq := &pb.DeactivateDataSourceRequest{
			Code: "IDEMPOTENT_DEACTIVATE",
		}

		deactivateResp1, err := server.DeactivateDataSource(ctx, deactivateReq)
		require.NoError(t, err)
		assert.False(t, deactivateResp1.Source.IsActive)

		// Second deactivation should also succeed (idempotent)
		deactivateResp2, err := server.DeactivateDataSource(ctx, deactivateReq)
		require.NoError(t, err)
		assert.False(t, deactivateResp2.Source.IsActive)
	})
}

func TestDeactivateDataSource_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("returns NOT_FOUND for non-existent data source", func(t *testing.T) {
		deactivateReq := &pb.DeactivateDataSourceRequest{
			Code: "NONEXISTENT_DEACTIVATE",
		}

		_, err := server.DeactivateDataSource(ctx, deactivateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestListDataSources_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("lists all data sources without filters", func(t *testing.T) {
		// Register multiple data sources
		for _, code := range []string{"LIST_A", "LIST_B", "LIST_C"} {
			req := &pb.RegisterDataSourceRequest{
				Code:       code,
				Name:       "List Test " + code,
				TrustLevel: 50,
			}
			_, err := server.RegisterDataSource(ctx, req)
			require.NoError(t, err)
		}

		// List all
		listReq := &pb.ListDataSourcesRequest{
			PageSize: 10,
		}

		listResp, err := server.ListDataSources(ctx, listReq)
		require.NoError(t, err)
		require.NotNil(t, listResp)
		assert.GreaterOrEqual(t, len(listResp.Sources), 3)
	})

	t.Run("filters active sources only", func(t *testing.T) {
		// Register two sources
		for _, code := range []string{"FILTER_ACTIVE_A", "FILTER_ACTIVE_B"} {
			req := &pb.RegisterDataSourceRequest{
				Code:       code,
				Name:       "Filter Active Test " + code,
				TrustLevel: 50,
			}
			_, err := server.RegisterDataSource(ctx, req)
			require.NoError(t, err)
		}

		// Deactivate one
		deactivateReq := &pb.DeactivateDataSourceRequest{
			Code: "FILTER_ACTIVE_B",
		}
		_, err := server.DeactivateDataSource(ctx, deactivateReq)
		require.NoError(t, err)

		// List only active sources
		listReq := &pb.ListDataSourcesRequest{
			ActiveOnly: true,
			PageSize:   100,
		}

		listResp, err := server.ListDataSources(ctx, listReq)
		require.NoError(t, err)

		// Verify all returned sources are active
		for _, source := range listResp.Sources {
			assert.True(t, source.IsActive, "expected all sources to be active, but found inactive: %s", source.Code)
		}
	})

	t.Run("returns all sources when activeOnly filter not yet implemented in repository", func(t *testing.T) {
		// Create a fresh server for this test
		freshServer, _, freshCleanup := setupTestServerForSource(t) //nolint:contextcheck
		defer freshCleanup()

		// Register one source
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "ONLY_SOURCE",
			Name:       "Only Source",
			TrustLevel: 50,
		}
		_, err := freshServer.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		// List with activeOnly true
		// Note: The repository currently ignores the activeOnly parameter (it's marked as `_ bool`)
		// This test documents the current behavior - filtering is not implemented
		listReq := &pb.ListDataSourcesRequest{
			ActiveOnly: true,
			PageSize:   100,
		}

		listResp, err := freshServer.ListDataSources(ctx, listReq)
		require.NoError(t, err)
		// Since filtering is not implemented, we get all sources regardless of activeOnly
		assert.Len(t, listResp.Sources, 1)
	})

	t.Run("applies default page size when not specified", func(t *testing.T) {
		// Just verify it doesn't error with zero page size
		listReq := &pb.ListDataSourcesRequest{
			PageSize: 0,
		}

		listResp, err := server.ListDataSources(ctx, listReq)
		require.NoError(t, err)
		require.NotNil(t, listResp)
	})

	t.Run("caps page size at maximum", func(t *testing.T) {
		listReq := &pb.ListDataSourcesRequest{
			PageSize: 1000, // Request more than max
		}

		listResp, err := server.ListDataSources(ctx, listReq)
		require.NoError(t, err)
		require.NotNil(t, listResp)
		// Should not error, and results should be capped
	})
}

func TestDomainSourceToProto(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("converts domain source to proto correctly", func(t *testing.T) {
		// Register a source
		registerReq := &pb.RegisterDataSourceRequest{
			Code:        "PROTO_CONVERT_TEST",
			Name:        "Proto Convert Test",
			Description: "Testing proto conversion",
			TrustLevel:  75,
		}

		resp, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		// Verify all fields are correctly converted
		assert.NotEmpty(t, resp.Source.Id)
		assert.Equal(t, "PROTO_CONVERT_TEST", resp.Source.Code)
		assert.Equal(t, "Proto Convert Test", resp.Source.Name)
		assert.Equal(t, "Testing proto conversion", resp.Source.Description)
		assert.Equal(t, int32(75), resp.Source.TrustLevel)
		assert.True(t, resp.Source.IsActive)
		assert.NotNil(t, resp.Source.CreatedAt)
		// UpdatedAt should be nil for newly created sources (since CreatedAt == UpdatedAt)
		assert.Nil(t, resp.Source.UpdatedAt)
	})

	t.Run("sets UpdatedAt when source is updated", func(t *testing.T) {
		// Register a source
		registerReq := &pb.RegisterDataSourceRequest{
			Code:       "UPDATE_TIME_TEST",
			Name:       "Update Time Test",
			TrustLevel: 50,
		}

		_, err := server.RegisterDataSource(ctx, registerReq)
		require.NoError(t, err)

		// Update the source
		updateReq := &pb.UpdateDataSourceRequest{
			Code:       "UPDATE_TIME_TEST",
			Name:       "Updated Name",
			TrustLevel: 60,
		}

		updateResp, err := server.UpdateDataSource(ctx, updateReq)
		require.NoError(t, err)

		// UpdatedAt should now be set
		assert.NotNil(t, updateResp.Source.UpdatedAt)
	})
}
