package refdata

import (
	"context"
	"testing"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockReferenceDataClient implements referencedatav1.ReferenceDataServiceClient for testing.
type mockReferenceDataClient struct {
	referencedatav1.ReferenceDataServiceClient
	instruments map[string]*referencedatav1.InstrumentDefinition
}

func (m *mockReferenceDataClient) RetrieveInstrument(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
	inst, ok := m.instruments[req.Code]
	if !ok {
		return nil, status.Error(codes.NotFound, "instrument not found")
	}
	return &referencedatav1.RetrieveInstrumentResponse{Instrument: inst}, nil
}

func (m *mockReferenceDataClient) ListInstruments(_ context.Context, _ *referencedatav1.ListInstrumentsRequest, _ ...grpc.CallOption) (*referencedatav1.ListInstrumentsResponse, error) {
	instruments := make([]*referencedatav1.InstrumentDefinition, 0, len(m.instruments))
	for _, inst := range m.instruments {
		instruments = append(instruments, inst)
	}
	return &referencedatav1.ListInstrumentsResponse{Instruments: instruments}, nil
}

func TestDimensionToString(t *testing.T) {
	tests := []struct {
		dimension referencedatav1.Dimension
		expected  string
	}{
		{referencedatav1.Dimension_DIMENSION_CURRENCY, "CURRENCY"},
		{referencedatav1.Dimension_DIMENSION_ENERGY, "ENERGY"},
		{referencedatav1.Dimension_DIMENSION_COMPUTE, "COMPUTE"},
		{referencedatav1.Dimension_DIMENSION_CARBON, "CARBON"},
		{referencedatav1.Dimension_DIMENSION_COUNT, "COUNT"},
		{referencedatav1.Dimension_DIMENSION_UNSPECIFIED, "UNSPECIFIED"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := dimensionToString(tt.dimension)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProtoToProperties(t *testing.T) {
	inst := &referencedatav1.InstrumentDefinition{
		Code:      "KWH",
		Dimension: referencedatav1.Dimension_DIMENSION_ENERGY,
		Precision: 6,
		Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		CreatedAt: timestamppb.Now(),
	}

	props := protoToProperties(inst)

	assert.Equal(t, "KWH", props.Code)
	assert.Equal(t, "ENERGY", props.Dimension)
	assert.Equal(t, 6, props.Precision)
	assert.Equal(t, DefaultRoundingMode, props.RoundingMode)
}

func TestGRPCDataSource_FetchInstrument(t *testing.T) {
	client := &mockReferenceDataClient{
		instruments: map[string]*referencedatav1.InstrumentDefinition{
			"KWH": {
				Code:      "KWH",
				Dimension: referencedatav1.Dimension_DIMENSION_ENERGY,
				Precision: 6,
				CreatedAt: timestamppb.Now(),
			},
		},
	}
	ds := NewGRPCDataSource(client)
	ctx := context.Background()

	t.Run("known instrument", func(t *testing.T) {
		props, err := ds.FetchInstrument(ctx, "KWH")
		require.NoError(t, err)
		assert.Equal(t, "KWH", props.Code)
		assert.Equal(t, "ENERGY", props.Dimension)
		assert.Equal(t, 6, props.Precision)
	})

	t.Run("unknown instrument", func(t *testing.T) {
		_, err := ds.FetchInstrument(ctx, "UNKNOWN")
		require.ErrorIs(t, err, ErrUnknownInstrument)
	})
}

func TestGRPCDataSource_FetchAllActive(t *testing.T) {
	client := &mockReferenceDataClient{
		instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:      "USD",
				Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
				Precision: 2,
				CreatedAt: timestamppb.Now(),
			},
			"KWH": {
				Code:      "KWH",
				Dimension: referencedatav1.Dimension_DIMENSION_ENERGY,
				Precision: 6,
				CreatedAt: timestamppb.Now(),
			},
		},
	}
	ds := NewGRPCDataSource(client)
	ctx := context.Background()

	instruments, err := ds.FetchAllActive(ctx)
	require.NoError(t, err)
	assert.Len(t, instruments, 2)

	// Verify all instruments are present (order not guaranteed with map iteration)
	codes := make(map[string]bool)
	for _, inst := range instruments {
		codes[inst.Code] = true
	}
	assert.True(t, codes["USD"])
	assert.True(t, codes["KWH"])
}
