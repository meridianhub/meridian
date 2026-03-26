package cache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	miclient "github.com/meridianhub/meridian/services/market-information/client"
)

func TestProtoToObservation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	validFrom := now.Add(-time.Hour)
	validTo := now

	proto := &marketinformationv1.MarketPriceObservation{
		Id:                 "obs-id",
		DatasetCode:        "ELEC_FORWARD",
		DatasetVersion:     1,
		ResolutionKeyValue: "PEAK_2026Q1",
		ObservedAt:         timestamppb.New(now),
		ValidFrom:          timestamppb.New(validFrom),
		ValidTo:            timestamppb.New(validTo),
		Value:              "45.50",
		Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		SourceId:           "source-123",
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "region", Value: "UK"},
			{Key: "zone", Value: "North"},
		},
	}

	obs, err := protoToObservation(proto)
	require.NoError(t, err)

	assert.True(t, decimal.RequireFromString("45.50").Equal(obs.Value))
	assert.Equal(t, "ELEC_FORWARD", obs.DataSetCode)
	assert.Equal(t, "source-123", obs.SourceID)
	assert.Equal(t, "QUALITY_LEVEL_ESTIMATE", obs.Quality)
	assert.Equal(t, now, obs.ObservedAt)
	assert.Equal(t, validFrom, obs.ValidFrom)
	assert.Equal(t, validTo, obs.ValidTo)
	assert.Equal(t, "UK", obs.Metadata["region"])
	assert.Equal(t, "North", obs.Metadata["zone"])
}

func TestProtoToObservation_InvalidValue(t *testing.T) {
	proto := &marketinformationv1.MarketPriceObservation{
		Value: "not-a-number",
	}

	_, err := protoToObservation(proto)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse observation value")
}

func TestProtoToObservation_NoAttributes(t *testing.T) {
	proto := &marketinformationv1.MarketPriceObservation{
		Value:       "10.00",
		DatasetCode: "TEST",
		Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
	}

	obs, err := protoToObservation(proto)
	require.NoError(t, err)
	assert.Nil(t, obs.Metadata)
}

func TestNewMDSSource(t *testing.T) {
	source := NewMDSSource(nil, "ELEC_FORWARD", "GBP/kWh")
	assert.Equal(t, "ELEC_FORWARD", source.datasetCode)
	assert.Equal(t, "GBP/kWh", source.unit)
}

// testMIServer is a minimal MarketInformationService implementation for gRPC testing.
type testMIServer struct {
	marketinformationv1.UnimplementedMarketInformationServiceServer
	listResp *marketinformationv1.ListObservationsResponse
	listErr  error
}

func (m *testMIServer) ListObservations(_ context.Context, _ *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listResp, nil
}

// setupTestMDSSource creates an in-process gRPC server using bufconn and returns
// an MDSSource backed by it, along with a cleanup function.
func setupTestMDSSource(t *testing.T, srv *testMIServer, datasetCode, unit string) (*MDSSource, func()) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	marketinformationv1.RegisterMarketInformationServiceServer(grpcServer, srv)
	go func() { _ = grpcServer.Serve(lis) }()

	bufDialer := func(_ context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	}

	client, cleanup, err := miclient.New(context.Background(), miclient.Config{
		Target: "passthrough:///bufnet",
		DialOptions: []grpc.DialOption{
			grpc.WithContextDialer(bufDialer),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	if err != nil {
		grpcServer.Stop()
		_ = lis.Close()
		t.Fatalf("failed to create miclient: %v", err)
	}

	source := NewMDSSource(client, datasetCode, unit)

	return source, func() {
		_ = cleanup()
		grpcServer.Stop()
		_ = lis.Close()
	}
}

func TestMDSSource_GetForwardPrice_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := &testMIServer{
		listResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{
					Value:       "123.45",
					DatasetCode: "ELEC_FORWARD",
					SourceId:    "src-1",
					Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
					ObservedAt:  timestamppb.New(now),
				},
			},
		},
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	obs, err := source.GetForwardPrice(context.Background(), "PEAK_2026Q1", now)
	require.NoError(t, err)
	require.NotNil(t, obs)
	assert.True(t, decimal.RequireFromString("123.45").Equal(obs.Value))
	assert.Equal(t, "GBP/kWh", obs.Unit)
	assert.Equal(t, "ELEC_FORWARD", obs.DataSetCode)
}

func TestMDSSource_GetForwardPrice_NotFound(t *testing.T) {
	srv := &testMIServer{
		listResp: &marketinformationv1.ListObservationsResponse{
			Observations: nil,
		},
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	_, err := source.GetForwardPrice(context.Background(), "PEAK_2026Q1", time.Now())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrObservationNotFound)
}

func TestMDSSource_GetForwardPrice_GRPCError(t *testing.T) {
	srv := &testMIServer{
		listErr: status.Error(codes.Internal, "database unavailable"),
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	_, err := source.GetForwardPrice(context.Background(), "PEAK_2026Q1", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query MDS for forward price")
}

func TestMDSSource_GetForwardPrice_MalformedValue(t *testing.T) {
	srv := &testMIServer{
		listResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{
					Value:   "not-a-number",
					Quality: marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
				},
			},
		},
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	_, err := source.GetForwardPrice(context.Background(), "PEAK_2026Q1", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse observation value")
}

func TestMDSSource_GetForwardPriceRange_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := &testMIServer{
		listResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{
					Value:       "100.00",
					DatasetCode: "ELEC_FORWARD",
					SourceId:    "src-1",
					Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
					ObservedAt:  timestamppb.New(now),
				},
				{
					Value:       "105.00",
					DatasetCode: "ELEC_FORWARD",
					SourceId:    "src-2",
					Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
					ObservedAt:  timestamppb.New(now.Add(time.Hour)),
				},
			},
		},
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	observations, err := source.GetForwardPriceRange(context.Background(), "PEAK_2026Q1", now, now.Add(2*time.Hour))
	require.NoError(t, err)
	require.Len(t, observations, 2)
	assert.Equal(t, "GBP/kWh", observations[0].Unit)
	assert.Equal(t, "GBP/kWh", observations[1].Unit)
	assert.True(t, decimal.RequireFromString("100.00").Equal(observations[0].Value))
	assert.True(t, decimal.RequireFromString("105.00").Equal(observations[1].Value))
}

func TestMDSSource_GetForwardPriceRange_Empty(t *testing.T) {
	srv := &testMIServer{
		listResp: &marketinformationv1.ListObservationsResponse{
			Observations: nil,
		},
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	now := time.Now()
	observations, err := source.GetForwardPriceRange(context.Background(), "PEAK_2026Q1", now, now.Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, observations)
}

func TestMDSSource_GetForwardPriceRange_GRPCError(t *testing.T) {
	srv := &testMIServer{
		listErr: status.Error(codes.Unavailable, "service unavailable"),
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	now := time.Now()
	_, err := source.GetForwardPriceRange(context.Background(), "PEAK_2026Q1", now, now.Add(time.Hour))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query MDS for forward price range")
}

func TestMDSSource_GetForwardPriceRange_SkipsMalformedObservations(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	srv := &testMIServer{
		listResp: &marketinformationv1.ListObservationsResponse{
			Observations: []*marketinformationv1.MarketPriceObservation{
				{
					Value:       "100.00",
					DatasetCode: "ELEC_FORWARD",
					Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
					ObservedAt:  timestamppb.New(now),
				},
				{
					Value:      "bad-value", // malformed - will be skipped with a warning
					Quality:    marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
					ObservedAt: timestamppb.New(now.Add(time.Hour)),
				},
			},
		},
	}

	source, cleanup := setupTestMDSSource(t, srv, "ELEC_FORWARD", "GBP/kWh")
	defer cleanup()

	observations, err := source.GetForwardPriceRange(context.Background(), "PEAK_2026Q1", now, now.Add(2*time.Hour))
	require.NoError(t, err)
	// Malformed observation is skipped; only the valid one is returned
	require.Len(t, observations, 1)
	assert.True(t, decimal.RequireFromString("100.00").Equal(observations[0].Value))
	assert.Equal(t, "GBP/kWh", observations[0].Unit)
}
