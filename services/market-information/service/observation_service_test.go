package service

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func setupTestServerForObservation(t *testing.T) (*Server, *testhelpers.TestContainer, func()) {
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

// setupTestDataSetAndSource creates an active dataset and source for observation tests
func setupTestDataSetAndSource(t *testing.T, server *Server, ctx context.Context) (string, string) {
	t.Helper()

	// Register and activate a dataset
	datasetReq := &pb.RegisterDataSetRequest{
		Code:                    "TEST_OBS_DATASET",
		DisplayName:             "Test Observation Dataset",
		Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
		ValidationExpression:    "value > 0",
		ResolutionKeyExpression: "currency_pair",
	}
	datasetResp, err := server.RegisterDataSet(ctx, datasetReq)
	require.NoError(t, err)

	activateReq := &pb.ActivateDataSetRequest{
		Code:    "TEST_OBS_DATASET",
		Version: datasetResp.Dataset.Version,
	}
	_, err = server.ActivateDataSet(ctx, activateReq)
	require.NoError(t, err)

	// Register a data source
	sourceReq := &pb.RegisterDataSourceRequest{
		Code:        "TEST_SOURCE",
		Name:        "Test Data Source",
		Description: "Test source for observations",
		TrustLevel:  80,
	}
	sourceResp, err := server.RegisterDataSource(ctx, sourceReq)
	require.NoError(t, err)

	return "TEST_OBS_DATASET", sourceResp.Source.Code
}

func TestRecordObservation_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForObservation(t)
	defer cleanup()

	ctx := context.Background()
	datasetCode, sourceCode := setupTestDataSetAndSource(t, server, ctx)

	t.Run("successfully records observation", func(t *testing.T) {
		now := time.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: datasetCode,
			SourceCode:  sourceCode,
			Value:       "1.2345",
			ObservedAt:  timestamppb.New(now),
			ValidFrom:   timestamppb.New(now),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		}

		resp, err := server.RecordObservation(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Observation)

		assert.NotEmpty(t, resp.Observation.Id)
		assert.Equal(t, "1.2345", resp.Observation.Value)
		assert.Equal(t, pb.QualityLevel_QUALITY_LEVEL_ACTUAL, resp.Observation.Quality)
	})
}

func TestRecordObservation_Errors(t *testing.T) {
	server, _, cleanup := setupTestServerForObservation(t)
	defer cleanup()

	ctx := context.Background()
	datasetCode, sourceCode := setupTestDataSetAndSource(t, server, ctx)

	t.Run("returns NOT_FOUND for non-existent dataset", func(t *testing.T) {
		now := time.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: "NONEXISTENT_DATASET",
			SourceCode:  sourceCode,
			Value:       "1.0",
			ObservedAt:  timestamppb.New(now),
			ValidFrom:   timestamppb.New(now),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		}

		_, err := server.RecordObservation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns INVALID_ARGUMENT for missing observed_at", func(t *testing.T) {
		now := time.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: datasetCode,
			SourceCode:  sourceCode,
			Value:       "1.0",
			ObservedAt:  nil,
			ValidFrom:   timestamppb.New(now),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		}

		_, err := server.RecordObservation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "observed_at")
	})

	t.Run("returns INVALID_ARGUMENT for invalid decimal value", func(t *testing.T) {
		now := time.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: datasetCode,
			SourceCode:  sourceCode,
			Value:       "not-a-number",
			ObservedAt:  timestamppb.New(now),
			ValidFrom:   timestamppb.New(now),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		}

		_, err := server.RecordObservation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestRetrieveObservation_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForObservation(t)
	defer cleanup()

	ctx := context.Background()
	datasetCode, sourceCode := setupTestDataSetAndSource(t, server, ctx)

	t.Run("retrieves recorded observation by ID", func(t *testing.T) {
		// First, record an observation
		now := time.Now()
		recordReq := &pb.RecordObservationRequest{
			DatasetCode: datasetCode,
			SourceCode:  sourceCode,
			Value:       "3.14",
			ObservedAt:  timestamppb.New(now),
			ValidFrom:   timestamppb.New(now),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		}

		recordResp, err := server.RecordObservation(ctx, recordReq)
		require.NoError(t, err)

		// Retrieve it
		retrieveReq := &pb.RetrieveObservationRequest{
			ObservationId: recordResp.Observation.Id,
		}

		retrieveResp, err := server.RetrieveObservation(ctx, retrieveReq)
		require.NoError(t, err)
		require.NotNil(t, retrieveResp)
		require.NotNil(t, retrieveResp.Observation)

		assert.Equal(t, recordResp.Observation.Id, retrieveResp.Observation.Id)
		assert.Equal(t, "3.14", retrieveResp.Observation.Value)
	})
}

func TestListObservations_Success(t *testing.T) {
	server, _, cleanup := setupTestServerForObservation(t)
	defer cleanup()

	ctx := context.Background()
	datasetCode, sourceCode := setupTestDataSetAndSource(t, server, ctx)

	t.Run("lists observations for dataset", func(t *testing.T) {
		// Record multiple observations
		now := time.Now()
		for i := 1; i <= 3; i++ {
			req := &pb.RecordObservationRequest{
				DatasetCode: datasetCode,
				SourceCode:  sourceCode,
				Value:       "1.0",
				ObservedAt:  timestamppb.New(now.Add(time.Duration(i) * time.Minute)),
				ValidFrom:   timestamppb.New(now.Add(time.Duration(i) * time.Minute)),
				Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
				Attributes: []*quantityv1.AttributeEntry{
					{Key: "currency_pair", Value: "EUR/USD"},
				},
			}
			_, err := server.RecordObservation(ctx, req)
			require.NoError(t, err)
		}

		// List observations
		listReq := &pb.ListObservationsRequest{
			DatasetCode: datasetCode,
			PageSize:    10,
		}

		listResp, err := server.ListObservations(ctx, listReq)
		require.NoError(t, err)
		require.NotNil(t, listResp)

		assert.GreaterOrEqual(t, len(listResp.Observations), 3)
	})
}
