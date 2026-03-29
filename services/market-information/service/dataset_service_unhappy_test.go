package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func setupTestServerWithCelValidator(t *testing.T) (*Server, *testhelpers.TestContainer, func()) {
	t.Helper()

	tc := testhelpers.SetupTestContainer(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	validator, err := NewCelValidator()
	require.NoError(t, err)

	server, err := NewServer(
		tc.Repos.DataSet,
		tc.Repos.Observation,
		tc.Repos.Source,
		WithLogger(logger),
		WithCelValidator(validator),
	)
	require.NoError(t, err)

	cleanup := func() {
		tc.Cleanup(t)
	}

	return server, tc, cleanup
}

func TestValidateCelExpressions(t *testing.T) {
	server, _, cleanup := setupTestServerWithCelValidator(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("rejects invalid validation expression on register", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "INVALID_CEL_VALIDATION",
			DisplayName:             "Invalid CEL Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "invalid_func(x, y, z)",
			ResolutionKeyExpression: "has(observation_context.pair) ? observation_context.pair : 'default'",
		}

		_, err := server.RegisterDataSet(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "validation_expression")
	})

	t.Run("rejects invalid resolution key expression on register", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "INVALID_CEL_RESOLUTION",
			DisplayName:             "Invalid CEL Resolution",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "nonexistent_var + broken",
		}

		_, err := server.RegisterDataSet(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "resolution_key_expression")
	})

	t.Run("rejects invalid error message expression on register", func(t *testing.T) {
		req := &pb.RegisterDataSetRequest{
			Code:                    "INVALID_CEL_ERROR_MSG",
			DisplayName:             "Invalid CEL Error Msg",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "has(observation_context.pair) ? observation_context.pair : 'default'",
			ErrorMessageExpression:  "broken_func(nonexistent_var)",
		}

		_, err := server.RegisterDataSet(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "error_message_expression")
	})
}

func TestUpdateDataSet_CelValidation(t *testing.T) {
	server, _, cleanup := setupTestServerWithCelValidator(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("rejects invalid CEL expression on update", func(t *testing.T) {
		// Register a valid dataset first
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "UPDATE_CEL_TEST",
			DisplayName:             "Update CEL Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "decimal(value) > decimal('0')",
			ResolutionKeyExpression: "has(observation_context.pair) ? observation_context.pair : 'default'",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		// Update with invalid expression
		updateReq := &pb.UpdateDataSetRequest{
			Code:                 "UPDATE_CEL_TEST",
			Version:              registerResp.Dataset.Version,
			ValidationExpression: "broken_syntax >>>",
		}

		_, err = server.UpdateDataSet(ctx, updateReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("validates CEL expressions on activation", func(t *testing.T) {
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "ACTIVATE_CEL_TEST",
			DisplayName:             "Activate CEL Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "decimal(value) > decimal('0')",
			ResolutionKeyExpression: "has(observation_context.pair) ? observation_context.pair : 'default'",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		activateResp, err := server.ActivateDataSet(ctx, &pb.ActivateDataSetRequest{
			Code:    "ACTIVATE_CEL_TEST",
			Version: registerResp.Dataset.Version,
		})
		require.NoError(t, err)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_ACTIVE, activateResp.Dataset.Status)
	})
}

func TestDeprecateDataSet_FromDraft(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("deprecates draft dataset", func(t *testing.T) {
		registerReq := &pb.RegisterDataSetRequest{
			Code:                    "DEPRECATE_DRAFT_TEST",
			DisplayName:             "Deprecate Draft Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		}

		registerResp, err := server.RegisterDataSet(ctx, registerReq)
		require.NoError(t, err)

		deprecateResp, err := server.DeprecateDataSet(ctx, &pb.DeprecateDataSetRequest{
			Code:    "DEPRECATE_DRAFT_TEST",
			Version: registerResp.Dataset.Version,
		})
		require.NoError(t, err)
		assert.Equal(t, pb.DataSetStatus_DATA_SET_STATUS_DEPRECATED, deprecateResp.Dataset.Status)
	})
}

func TestProtoStatusToDomain(t *testing.T) {
	tests := []struct {
		name        string
		input       pb.DataSetStatus
		expected    domain.DataSetStatus
		expectError bool
	}{
		{"unspecified returns error", pb.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED, "", true},
		{"draft", pb.DataSetStatus_DATA_SET_STATUS_DRAFT, domain.DataSetStatusDraft, false},
		{"active", pb.DataSetStatus_DATA_SET_STATUS_ACTIVE, domain.DataSetStatusActive, false},
		{"deprecated", pb.DataSetStatus_DATA_SET_STATUS_DEPRECATED, domain.DataSetStatusDeprecated, false},
		{"unknown value returns error", pb.DataSetStatus(999), "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoStatusToDomain(tt.input)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestDomainStatusToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    domain.DataSetStatus
		expected pb.DataSetStatus
	}{
		{"draft", domain.DataSetStatusDraft, pb.DataSetStatus_DATA_SET_STATUS_DRAFT},
		{"active", domain.DataSetStatusActive, pb.DataSetStatus_DATA_SET_STATUS_ACTIVE},
		{"deprecated", domain.DataSetStatusDeprecated, pb.DataSetStatus_DATA_SET_STATUS_DEPRECATED},
		{"unknown returns unspecified", domain.DataSetStatus("UNKNOWN"), pb.DataSetStatus_DATA_SET_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domainStatusToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDomainCategoryToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    domain.DataCategory
		expected pb.DataCategory
	}{
		{"pricing maps to fx rate", domain.DataCategoryPricing, pb.DataCategory_DATA_CATEGORY_FX_RATE},
		{"contextual maps to index value", domain.DataCategoryContextual, pb.DataCategory_DATA_CATEGORY_INDEX_VALUE},
		{"utilization maps to index value", domain.DataCategoryUtilization, pb.DataCategory_DATA_CATEGORY_INDEX_VALUE},
		{"unknown maps to unspecified", domain.DataCategory("UNKNOWN"), pb.DataCategory_DATA_CATEGORY_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domainCategoryToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProtoCategoryToDomain(t *testing.T) {
	tests := []struct {
		name        string
		input       pb.DataCategory
		expected    domain.DataCategory
		expectError bool
	}{
		{"unspecified returns error", pb.DataCategory_DATA_CATEGORY_UNSPECIFIED, "", true},
		{"fx rate maps to pricing", pb.DataCategory_DATA_CATEGORY_FX_RATE, domain.DataCategoryPricing, false},
		{"interest rate maps to pricing", pb.DataCategory_DATA_CATEGORY_INTEREST_RATE, domain.DataCategoryPricing, false},
		{"commodity price maps to pricing", pb.DataCategory_DATA_CATEGORY_COMMODITY_PRICE, domain.DataCategoryPricing, false},
		{"equity price maps to pricing", pb.DataCategory_DATA_CATEGORY_EQUITY_PRICE, domain.DataCategoryPricing, false},
		{"energy price maps to pricing", pb.DataCategory_DATA_CATEGORY_ENERGY_PRICE, domain.DataCategoryPricing, false},
		{"carbon price maps to pricing", pb.DataCategory_DATA_CATEGORY_CARBON_PRICE, domain.DataCategoryPricing, false},
		{"benchmark rate maps to pricing", pb.DataCategory_DATA_CATEGORY_BENCHMARK_RATE, domain.DataCategoryPricing, false},
		{"credit spread maps to pricing", pb.DataCategory_DATA_CATEGORY_CREDIT_SPREAD, domain.DataCategoryPricing, false},
		{"index value maps to contextual", pb.DataCategory_DATA_CATEGORY_INDEX_VALUE, domain.DataCategoryContextual, false},
		{"volatility maps to contextual", pb.DataCategory_DATA_CATEGORY_VOLATILITY, domain.DataCategoryContextual, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoCategoryToDomain(tt.input)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestListDataSets_InvalidStatusFilter(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("rejects invalid status filter", func(t *testing.T) {
		listReq := &pb.ListDataSetsRequest{
			StatusFilter: pb.DataSetStatus(999),
			PageSize:     10,
		}

		_, err := server.ListDataSets(ctx, listReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestListDataSets_Pagination(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("rejects negative page size", func(t *testing.T) {
		listReq := &pb.ListDataSetsRequest{
			PageSize: -1,
		}

		_, err := server.ListDataSets(ctx, listReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "page_size")
	})

	t.Run("caps page size at maximum", func(t *testing.T) {
		listReq := &pb.ListDataSetsRequest{
			PageSize: 500, // Exceeds max of 100
		}

		resp, err := server.ListDataSets(ctx, listReq)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("applies default page size", func(t *testing.T) {
		listReq := &pb.ListDataSetsRequest{
			PageSize: 0,
		}

		resp, err := server.ListDataSets(ctx, listReq)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("rejects invalid page token", func(t *testing.T) {
		listReq := &pb.ListDataSetsRequest{
			PageSize:  10,
			PageToken: "invalid-token-!!!",
		}

		_, err := server.ListDataSets(ctx, listReq)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "page_token")
	})
}

func TestMapDataSetDomainError(t *testing.T) {
	server, _, cleanup := setupTestServerForDataSet(t)
	defer cleanup()

	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"not found", domain.ErrDataSetNotFound, codes.NotFound},
		{"duplicate code", domain.ErrDuplicateDataSetCode, codes.AlreadyExists},
		{"invalid transition", domain.ErrInvalidStatusTransition, codes.FailedPrecondition},
		{"deprecated", domain.ErrDataSetDeprecated, codes.FailedPrecondition},
		{"version mismatch", domain.ErrVersionMismatch, codes.Aborted},
		{"code required", domain.ErrCodeRequired, codes.InvalidArgument},
		{"name required", domain.ErrNameRequired, codes.InvalidArgument},
		{"validation expr required", domain.ErrValidationExpressionRequired, codes.InvalidArgument},
		{"resolution key expr required", domain.ErrResolutionKeyExpressionRequired, codes.InvalidArgument},
		{"invalid category", domain.ErrInvalidDataCategory, codes.InvalidArgument},
		{"invalid status", domain.ErrInvalidDataSetStatus, codes.InvalidArgument},
		{"unknown error", errors.New("some unknown error"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.mapDataSetDomainError(tt.err, "TestOp", "TEST_CODE")
			st, ok := status.FromError(result)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}
