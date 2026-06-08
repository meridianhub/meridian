package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

func setupTestServerWithCel(t *testing.T) (*Server, *testhelpers.TestContainer, func()) {
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

func setupActiveDatasetAndSource(t *testing.T, server *Server, ctx context.Context, dsCode, srcCode string) {
	t.Helper()

	// Register and activate dataset
	registerResp, err := server.RegisterDataSet(ctx, &pb.RegisterDataSetRequest{
		Code:                    dsCode,
		DisplayName:             "Test Dataset " + dsCode,
		Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
		ValidationExpression:    "decimal(value) > decimal('0')",
		ResolutionKeyExpression: "has(observation_context.currency_pair) ? observation_context.currency_pair : 'default'",
		ErrorMessageExpression:  "'Value ' + value + ' failed validation for dataset ' + dataset_code",
	})
	require.NoError(t, err)

	_, err = server.ActivateDataSet(ctx, &pb.ActivateDataSetRequest{
		Code:    dsCode,
		Version: registerResp.Dataset.Version,
	})
	require.NoError(t, err)

	// Register source
	_, err = server.RegisterDataSource(ctx, &pb.RegisterDataSourceRequest{
		Code:       srcCode,
		Name:       "Test Source " + srcCode,
		TrustLevel: 80,
	})
	require.NoError(t, err)
}

func TestRecordObservation_CELValidation(t *testing.T) {
	server, _, cleanup := setupTestServerWithCel(t)
	defer cleanup()

	ctx := context.Background()
	dsCode := "CEL_VALIDATION_DS"
	srcCode := "CEL_VALIDATION_SRC"
	setupActiveDatasetAndSource(t, server, ctx, dsCode, srcCode)

	t.Run("rejects observation failing CEL validation", func(t *testing.T) {
		now := timestamppb.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "-1.0",
			ObservedAt:  now,
			ValidFrom:   now,
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

	t.Run("computes resolution key from CEL expression", func(t *testing.T) {
		now := timestamppb.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "1.1234",
			ObservedAt:  now,
			ValidFrom:   now,
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		}

		resp, err := server.RecordObservation(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "EUR/USD", resp.Observation.ResolutionKeyValue)
	})

	t.Run("uses default resolution key when no context provided", func(t *testing.T) {
		now := timestamppb.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "1.0",
			ObservedAt:  now,
			ValidFrom:   now,
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		}

		resp, err := server.RecordObservation(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "default", resp.Observation.ResolutionKeyValue)
	})
}

func TestRecordObservation_NilObservedAt(t *testing.T) {
	server, _, cleanup := setupTestServerWithCel(t)
	defer cleanup()

	ctx := context.Background()
	dsCode := "NIL_TS_DS"
	srcCode := "NIL_TS_SRC"
	setupActiveDatasetAndSource(t, server, ctx, dsCode, srcCode)

	t.Run("rejects nil observed_at", func(t *testing.T) {
		req := &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "1.0",
			ObservedAt:  nil,
			ValidFrom:   timestamppb.Now(),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		}

		_, err := server.RecordObservation(ctx, req)
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "observed_at")
	})
}

func TestRecordObservation_WithExplicitValidTo(t *testing.T) {
	server, _, cleanup := setupTestServerWithCel(t)
	defer cleanup()

	ctx := context.Background()
	dsCode := "VALID_TO_DS"
	srcCode := "VALID_TO_SRC"
	setupActiveDatasetAndSource(t, server, ctx, dsCode, srcCode)

	t.Run("records observation with explicit valid_to", func(t *testing.T) {
		now := time.Now()
		req := &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "1.5",
			ObservedAt:  timestamppb.New(now),
			ValidFrom:   timestamppb.New(now),
			ValidTo:     timestamppb.New(now.Add(48 * time.Hour)),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "GBP/USD"},
			},
		}

		resp, err := server.RecordObservation(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, resp.Observation)
	})
}

func TestComputeResolutionKey_WithoutValidator(t *testing.T) {
	// Create server without CEL validator
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	server, err := NewServer(
		tc.Repos.DataSet,
		tc.Repos.Observation,
		tc.Repos.Source,
		WithLogger(logger),
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Register and activate dataset (no CEL validation since no validator)
	registerResp, err := server.RegisterDataSet(ctx, &pb.RegisterDataSetRequest{
		Code:                    "NO_CEL_DS",
		DisplayName:             "No CEL Dataset",
		Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
		ValidationExpression:    "true",
		ResolutionKeyExpression: "key",
	})
	require.NoError(t, err)

	_, err = server.ActivateDataSet(ctx, &pb.ActivateDataSetRequest{
		Code:    "NO_CEL_DS",
		Version: registerResp.Dataset.Version,
	})
	require.NoError(t, err)

	_, err = server.RegisterDataSource(ctx, &pb.RegisterDataSourceRequest{
		Code:       "NO_CEL_SRC",
		Name:       "No CEL Source",
		TrustLevel: 50,
	})
	require.NoError(t, err)

	t.Run("uses context value as resolution key without validator", func(t *testing.T) {
		now := timestamppb.Now()
		resp, err := server.RecordObservation(ctx, &pb.RecordObservationRequest{
			DatasetCode: "NO_CEL_DS",
			SourceCode:  "NO_CEL_SRC",
			Value:       "1.0",
			ObservedAt:  now,
			ValidFrom:   now,
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "EUR/USD", resp.Observation.ResolutionKeyValue)
	})

	t.Run("uses default resolution key without context", func(t *testing.T) {
		now := timestamppb.Now()
		resp, err := server.RecordObservation(ctx, &pb.RecordObservationRequest{
			DatasetCode: "NO_CEL_DS",
			SourceCode:  "NO_CEL_SRC",
			Value:       "2.0",
			ObservedAt:  now,
			ValidFrom:   now,
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		})
		require.NoError(t, err)
		assert.Equal(t, "default", resp.Observation.ResolutionKeyValue)
	})
}

func TestProtoQualityLevelToDomain(t *testing.T) {
	tests := []struct {
		name     string
		input    pb.QualityLevel
		expected domain.QualityLevel
	}{
		{"unspecified defaults to estimate", pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED, domain.QualityLevelEstimate},
		{"estimate", pb.QualityLevel_QUALITY_LEVEL_ESTIMATE, domain.QualityLevelEstimate},
		{"provisional", pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL, domain.QualityLevelProvisional},
		{"actual", pb.QualityLevel_QUALITY_LEVEL_ACTUAL, domain.QualityLevelActual},
		// Proto slot 4 is still spelled REVISED but is semantically VERIFIED (rename pending task 14).
		{"revised slot maps to verified", pb.QualityLevel_QUALITY_LEVEL_REVISED, domain.QualityLevelVerified},
		{"unknown defaults to estimate", pb.QualityLevel(999), domain.QualityLevelEstimate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := protoQualityLevelToDomain(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDomainQualityLevelToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    domain.QualityLevel
		expected pb.QualityLevel
	}{
		{"estimate", domain.QualityLevelEstimate, pb.QualityLevel_QUALITY_LEVEL_ESTIMATE},
		{"provisional", domain.QualityLevelProvisional, pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL},
		{"actual", domain.QualityLevelActual, pb.QualityLevel_QUALITY_LEVEL_ACTUAL},
		// VERIFIED maps onto proto slot 4 (still spelled REVISED, semantically VERIFIED; rename pending task 14).
		{"verified maps to revised slot", domain.QualityLevelVerified, pb.QualityLevel_QUALITY_LEVEL_REVISED},
		{"unknown maps to unspecified", domain.QualityLevel(99), pb.QualityLevel_QUALITY_LEVEL_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domainQualityLevelToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestQualityLevel_ProtoDomainRoundTrip is the golden lossless round-trip: every
// domain confidence grade must survive domain -> proto -> domain as the identity.
// This guards against a regression to the old lossy mapping (where VERIFIED and
// PROVISIONAL collapsed onto other levels and lost information).
func TestQualityLevel_ProtoDomainRoundTrip(t *testing.T) {
	levels := []domain.QualityLevel{
		domain.QualityLevelEstimate,
		domain.QualityLevelProvisional,
		domain.QualityLevelActual,
		domain.QualityLevelVerified,
	}

	for _, level := range levels {
		t.Run(level.String(), func(t *testing.T) {
			roundTripped := protoQualityLevelToDomain(domainQualityLevelToProto(level))
			assert.Equal(t, level, roundTripped,
				"domain -> proto -> domain must be identity for %s", level)
		})
	}
}

func TestMapObservationDomainError(t *testing.T) {
	server, _, cleanup := setupTestServerForObservation(t)
	defer cleanup()

	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"observation not found", domain.ErrObservationNotFound, codes.NotFound},
		{"dataset not found", domain.ErrDataSetNotFound, codes.NotFound},
		{"source not found", domain.ErrDataSourceNotFound, codes.NotFound},
		{"dataset deprecated", domain.ErrDataSetDeprecated, codes.FailedPrecondition},
		{"invalid temporal bounds", domain.ErrInvalidTemporalBounds, codes.InvalidArgument},
		{"invalid quality level", domain.ErrInvalidQualityLevel, codes.InvalidArgument},
		{"dataset code required", domain.ErrDataSetCodeRequired, codes.InvalidArgument},
		{"source ID required", domain.ErrSourceIDRequired, codes.InvalidArgument},
		{"resolution key required", domain.ErrResolutionKeyRequired, codes.InvalidArgument},
		{"unit required", domain.ErrUnitRequired, codes.InvalidArgument},
		{"causation ID required", domain.ErrCausationIDRequired, codes.InvalidArgument},
		{"invalid trust level", domain.ErrInvalidTrustLevel, codes.InvalidArgument},
		{"unknown error", errors.New("unknown error"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.mapObservationDomainError(tt.err, "TestOp", "test-id")
			st, ok := status.FromError(result)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}

func TestMapSourceDomainError(t *testing.T) {
	server, _, cleanup := setupTestServerForSource(t)
	defer cleanup()

	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"source not found", domain.ErrDataSourceNotFound, codes.NotFound},
		{"duplicate code", domain.ErrDuplicateDataSourceCode, codes.AlreadyExists},
		{"code required", domain.ErrDataSourceCodeRequired, codes.InvalidArgument},
		{"name required", domain.ErrDataSourceNameRequired, codes.InvalidArgument},
		{"invalid source type", domain.ErrInvalidSourceType, codes.InvalidArgument},
		{"invalid trust level", domain.ErrInvalidTrustLevel, codes.InvalidArgument},
		{"unknown error", errors.New("unknown error"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.mapSourceDomainError(tt.err, "TestOp", "test-code")
			st, ok := status.FromError(result)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}

func TestListObservations_Pagination(t *testing.T) {
	server, _, cleanup := setupTestServerForObservation(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("caps page size at maximum", func(t *testing.T) {
		// Need a real dataset for the query to work
		registerResp, err := server.RegisterDataSet(ctx, &pb.RegisterDataSetRequest{
			Code:                    "PAGE_CAP_DS",
			DisplayName:             "Page Cap Test",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "key",
		})
		require.NoError(t, err)

		_, err = server.ActivateDataSet(ctx, &pb.ActivateDataSetRequest{
			Code:    "PAGE_CAP_DS",
			Version: registerResp.Dataset.Version,
		})
		require.NoError(t, err)

		resp, err := server.ListObservations(ctx, &pb.ListObservationsRequest{
			DatasetCode: "PAGE_CAP_DS",
			PageSize:    5000, // Exceeds max of 1000
		})
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, int32(0), resp.TotalCount)
	})

	t.Run("rejects invalid page token", func(t *testing.T) {
		_, err := server.ListObservations(ctx, &pb.ListObservationsRequest{
			DatasetCode: "PAGE_CAP_DS",
			PageSize:    10,
			PageToken:   "bad-token!!!",
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestRecordObservationBatch_WithCelValidation(t *testing.T) {
	server, _, cleanup := setupTestServerWithCel(t)
	defer cleanup()

	ctx := context.Background()
	dsCode := "BATCH_CEL_DS"
	srcCode := "BATCH_CEL_SRC"
	setupActiveDatasetAndSource(t, server, ctx, dsCode, srcCode)

	t.Run("batch with mixed valid and invalid values", func(t *testing.T) {
		now := timestamppb.Now()
		resp, err := server.RecordObservationBatch(ctx, &pb.RecordObservationBatchRequest{
			Observations: []*pb.BatchObservationEntry{
				{
					DatasetCode: dsCode,
					SourceCode:  srcCode,
					Value:       "1.5",
					ObservedAt:  now,
					ValidFrom:   now,
					Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
					Attributes: []*quantityv1.AttributeEntry{
						{Key: "currency_pair", Value: "EUR/USD"},
					},
					ClientReference: "good-1",
				},
				{
					DatasetCode: dsCode,
					SourceCode:  srcCode,
					Value:       "-1.0",
					ObservedAt:  now,
					ValidFrom:   now,
					Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
					Attributes: []*quantityv1.AttributeEntry{
						{Key: "currency_pair", Value: "GBP/USD"},
					},
					ClientReference: "bad-1",
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.TotalCount)
		assert.Equal(t, int32(1), resp.SuccessCount)
		assert.Equal(t, int32(1), resp.FailureCount)

		// Check individual results
		assert.True(t, resp.Results[0].Success)
		assert.Equal(t, "good-1", resp.Results[0].ClientReference)
		assert.False(t, resp.Results[1].Success)
		assert.Equal(t, "bad-1", resp.Results[1].ClientReference)
	})

	t.Run("batch with missing timestamps", func(t *testing.T) {
		resp, err := server.RecordObservationBatch(ctx, &pb.RecordObservationBatchRequest{
			Observations: []*pb.BatchObservationEntry{
				{
					DatasetCode: dsCode,
					SourceCode:  srcCode,
					Value:       "1.0",
					ObservedAt:  nil, // missing
					ValidFrom:   timestamppb.Now(),
					Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
				},
				{
					DatasetCode: dsCode,
					SourceCode:  srcCode,
					Value:       "2.0",
					ObservedAt:  timestamppb.Now(),
					ValidFrom:   nil, // missing
					Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(0), resp.SuccessCount)
		assert.Equal(t, int32(2), resp.FailureCount)
		assert.Contains(t, resp.Results[0].ErrorMessage, "observed_at")
		assert.Contains(t, resp.Results[1].ErrorMessage, "valid_from")
	})

	t.Run("batch with non-existent source", func(t *testing.T) {
		now := timestamppb.Now()
		resp, err := server.RecordObservationBatch(ctx, &pb.RecordObservationBatchRequest{
			Observations: []*pb.BatchObservationEntry{
				{
					DatasetCode: dsCode,
					SourceCode:  "NONEXISTENT_SRC",
					Value:       "1.0",
					ObservedAt:  now,
					ValidFrom:   now,
					Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(1), resp.FailureCount)
		assert.Contains(t, resp.Results[0].ErrorMessage, "source not found")
	})

	t.Run("batch with inactive dataset", func(t *testing.T) {
		// Register but don't activate
		_, err := server.RegisterDataSet(ctx, &pb.RegisterDataSetRequest{
			Code:                    "BATCH_DRAFT_DS",
			DisplayName:             "Batch Draft Dataset",
			Category:                pb.DataCategory_DATA_CATEGORY_FX_RATE,
			ValidationExpression:    "true",
			ResolutionKeyExpression: "'default'",
		})
		require.NoError(t, err)

		now := timestamppb.Now()
		resp, err := server.RecordObservationBatch(ctx, &pb.RecordObservationBatchRequest{
			Observations: []*pb.BatchObservationEntry{
				{
					DatasetCode: "BATCH_DRAFT_DS",
					SourceCode:  srcCode,
					Value:       "1.0",
					ObservedAt:  now,
					ValidFrom:   now,
					Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(1), resp.FailureCount)
		assert.Contains(t, resp.Results[0].ErrorMessage, "not active")
	})

	t.Run("batch with invalid decimal value", func(t *testing.T) {
		now := timestamppb.Now()
		resp, err := server.RecordObservationBatch(ctx, &pb.RecordObservationBatchRequest{
			Observations: []*pb.BatchObservationEntry{
				{
					DatasetCode: dsCode,
					SourceCode:  srcCode,
					Value:       "not-a-number",
					ObservedAt:  now,
					ValidFrom:   now,
					Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
				},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(1), resp.FailureCount)
		assert.Contains(t, resp.Results[0].ErrorMessage, "invalid decimal value")
	})
}

func TestRecordObservation_WithEventPublisher(t *testing.T) {
	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	validator, err := NewCelValidator()
	require.NoError(t, err)

	mock := &mockPublisher{}
	server, err := NewServer(
		tc.Repos.DataSet,
		tc.Repos.Observation,
		tc.Repos.Source,
		WithLogger(logger),
		WithCelValidator(validator),
		WithEventPublisher(mock),
	)
	require.NoError(t, err)

	ctx := context.Background()
	dsCode := "EVENT_PUB_DS"
	srcCode := "EVENT_PUB_SRC"
	setupActiveDatasetAndSource(t, server, ctx, dsCode, srcCode)

	t.Run("publishes event for ACTUAL quality", func(t *testing.T) {
		now := timestamppb.Now()
		_, err := server.RecordObservation(ctx, &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "1.5",
			ObservedAt:  now,
			ValidFrom:   now,
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 1, mock.publishCount)
	})

	t.Run("does not publish event for ESTIMATE quality", func(t *testing.T) {
		mock.publishCount = 0 // reset
		now := timestamppb.Now()
		_, err := server.RecordObservation(ctx, &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "2.5",
			ObservedAt:  now,
			ValidFrom:   now,
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "USD/JPY"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 0, mock.publishCount)
	})
}

// mockPublisher implements EventPublisher for testing event publishing.
type mockPublisher struct {
	publishCount int
	publishErr   error
}

func (m *mockPublisher) Publish(_ context.Context, _ any) error {
	if m.publishErr != nil {
		return m.publishErr
	}
	m.publishCount++
	return nil
}

func TestListObservations_WithFilters(t *testing.T) {
	server, _, cleanup := setupTestServerWithCel(t)
	defer cleanup()

	ctx := context.Background()
	dsCode := "FILTER_OBS_DS"
	srcCode := "FILTER_OBS_SRC"
	setupActiveDatasetAndSource(t, server, ctx, dsCode, srcCode)

	// Record some observations
	now := time.Now()
	for i := 0; i < 3; i++ {
		_, err := server.RecordObservation(ctx, &pb.RecordObservationRequest{
			DatasetCode: dsCode,
			SourceCode:  srcCode,
			Value:       "1.0",
			ObservedAt:  timestamppb.New(now.Add(time.Duration(i) * time.Hour)),
			ValidFrom:   timestamppb.New(now),
			Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "currency_pair", Value: "EUR/USD"},
			},
		})
		require.NoError(t, err)
	}

	t.Run("filters by quality level", func(t *testing.T) {
		resp, err := server.ListObservations(ctx, &pb.ListObservationsRequest{
			DatasetCode:   dsCode,
			QualityFilter: pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			PageSize:      100,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(resp.Observations), 3)
	})

	t.Run("filters by time range", func(t *testing.T) {
		resp, err := server.ListObservations(ctx, &pb.ListObservationsRequest{
			DatasetCode:  dsCode,
			ObservedFrom: timestamppb.New(now.Add(-1 * time.Hour)),
			ObservedTo:   timestamppb.New(now.Add(30 * time.Minute)),
			PageSize:     100,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(resp.Observations), 1)
	})

	t.Run("filters by resolution key", func(t *testing.T) {
		resp, err := server.ListObservations(ctx, &pb.ListObservationsRequest{
			DatasetCode:        dsCode,
			ResolutionKeyValue: "EUR/USD",
			PageSize:           100,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(resp.Observations), 3)
	})
}
