package client

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// mockGRPCClient implements the ReferenceDataServiceClient interface for testing.
type mockGRPCClient struct {
	retrieveFn func(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error)
}

func (m *mockGRPCClient) RegisterInstrument(_ context.Context, _ *referencedatav1.RegisterInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RegisterInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockGRPCClient) UpdateInstrument(_ context.Context, _ *referencedatav1.UpdateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.UpdateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockGRPCClient) RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if m.retrieveFn != nil {
		return m.retrieveFn(ctx, req)
	}
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockGRPCClient) ListInstruments(_ context.Context, _ *referencedatav1.ListInstrumentsRequest, _ ...grpc.CallOption) (*referencedatav1.ListInstrumentsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockGRPCClient) ActivateInstrument(_ context.Context, _ *referencedatav1.ActivateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.ActivateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockGRPCClient) DeprecateInstrument(_ context.Context, _ *referencedatav1.DeprecateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.DeprecateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockGRPCClient) EvaluateInstrument(_ context.Context, _ *referencedatav1.EvaluateInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.EvaluateInstrumentResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func (m *mockGRPCClient) GetAttributeSchema(_ context.Context, _ *referencedatav1.GetAttributeSchemaRequest, _ ...grpc.CallOption) (*referencedatav1.GetAttributeSchemaResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func TestGRPCSource_GetDefinition_Success(t *testing.T) {
	instrumentID := uuid.New()
	successorID := uuid.New()
	now := time.Now()
	activatedAt := now.Add(-24 * time.Hour)
	deprecatedAt := now.Add(-1 * time.Hour)

	mock := &mockGRPCClient{
		retrieveFn: func(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			assert.Equal(t, "USD", req.Code)
			assert.Equal(t, int32(1), req.Version)

			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:                       instrumentID.String(),
					Code:                     "USD",
					Version:                  1,
					Dimension:                referencedatav1.Dimension_DIMENSION_CURRENCY,
					Precision:                2,
					Status:                   referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED,
					IsSystem:                 true,
					ValidationExpression:     "amount > 0",
					FungibilityKeyExpression: "instrument_code",
					ErrorMessageExpression:   "error: invalid amount",
					AttributeSchema:          `{"type":"object"}`,
					DisplayName:              "US Dollar",
					Description:              "United States Dollar",
					CreatedAt:                timestamppb.New(now),
					ActivatedAt:              timestamppb.New(activatedAt),
					DeprecatedAt:             timestamppb.New(deprecatedAt),
					SuccessorId:              successorID.String(),
				},
			}, nil
		},
	}

	source := NewGRPCSource(mock)
	ctx := testdb.ContextWithTenant(t, "tenant1")

	def, err := source.GetDefinition(ctx, "USD", 1)
	require.NoError(t, err)
	require.NotNil(t, def)

	assert.Equal(t, instrumentID, def.ID)
	assert.Equal(t, "USD", def.Code)
	assert.Equal(t, 1, def.Version)
	assert.Equal(t, registry.DimensionMonetary, def.Dimension)
	assert.Equal(t, 2, def.Precision)
	assert.Equal(t, registry.StatusDeprecated, def.Status)
	assert.True(t, def.IsSystem)
	assert.Equal(t, "amount > 0", def.ValidationExpression)
	assert.Equal(t, "instrument_code", def.FungibilityKeyExpression)
	assert.Equal(t, "error: invalid amount", def.ErrorMessageExpression)
	assert.Equal(t, []byte(`{"type":"object"}`), def.AttributeSchema)
	assert.Equal(t, "US Dollar", def.DisplayName)
	assert.Equal(t, "United States Dollar", def.Description)
	assert.WithinDuration(t, now, def.CreatedAt, time.Second)
	require.NotNil(t, def.ActivatedAt)
	assert.WithinDuration(t, activatedAt, *def.ActivatedAt, time.Second)
	require.NotNil(t, def.DeprecatedAt)
	assert.WithinDuration(t, deprecatedAt, *def.DeprecatedAt, time.Second)
	require.NotNil(t, def.SuccessorID)
	assert.Equal(t, successorID, *def.SuccessorID)
}

func TestGRPCSource_GetDefinition_NotFound(t *testing.T) {
	mock := &mockGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return nil, status.Errorf(codes.NotFound, "instrument not found")
		},
	}

	source := NewGRPCSource(mock)
	ctx := testdb.ContextWithTenant(t, "tenant1")

	def, err := source.GetDefinition(ctx, "NOTFOUND", 1)
	assert.Nil(t, def)
	assert.ErrorIs(t, err, registry.ErrNotFound)
}

func TestGRPCSource_GetDefinition_OtherError(t *testing.T) {
	mock := &mockGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return nil, status.Errorf(codes.Internal, "internal error")
		},
	}

	source := NewGRPCSource(mock)
	ctx := testdb.ContextWithTenant(t, "tenant1")

	def, err := source.GetDefinition(ctx, "USD", 1)
	assert.Nil(t, def)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "grpc retrieve instrument")
}

func TestGRPCSource_GetDefinition_AllDimensions(t *testing.T) {
	tests := []struct {
		proto  referencedatav1.Dimension
		domain registry.Dimension
	}{
		{referencedatav1.Dimension_DIMENSION_CURRENCY, registry.DimensionMonetary},
		{referencedatav1.Dimension_DIMENSION_ENERGY, registry.DimensionEnergy},
		{referencedatav1.Dimension_DIMENSION_MASS, registry.DimensionMass},
		{referencedatav1.Dimension_DIMENSION_VOLUME, registry.DimensionVolume},
		{referencedatav1.Dimension_DIMENSION_TIME, registry.DimensionTime},
		{referencedatav1.Dimension_DIMENSION_COMPUTE, registry.DimensionCompute},
		{referencedatav1.Dimension_DIMENSION_COUNT, registry.DimensionQuantity},
		{referencedatav1.Dimension_DIMENSION_UNSPECIFIED, ""},
	}

	for _, tc := range tests {
		t.Run(tc.proto.String(), func(t *testing.T) {
			mock := &mockGRPCClient{
				retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
					return &referencedatav1.RetrieveInstrumentResponse{
						Instrument: &referencedatav1.InstrumentDefinition{
							Id:        uuid.New().String(),
							Code:      "TEST",
							Version:   1,
							Dimension: tc.proto,
							Status:    referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
							CreatedAt: timestamppb.Now(),
						},
					}, nil
				},
			}

			source := NewGRPCSource(mock)
			ctx := testdb.ContextWithTenant(t, "tenant1")

			def, err := source.GetDefinition(ctx, "TEST", 1)
			require.NoError(t, err)
			assert.Equal(t, tc.domain, def.Dimension)
		})
	}
}

func TestGRPCSource_GetDefinition_AllStatuses(t *testing.T) {
	tests := []struct {
		proto  referencedatav1.InstrumentStatus
		domain registry.Status
	}{
		{referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT, registry.StatusDraft},
		{referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE, registry.StatusActive},
		{referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED, registry.StatusDeprecated},
		{referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED, ""},
	}

	for _, tc := range tests {
		t.Run(tc.proto.String(), func(t *testing.T) {
			mock := &mockGRPCClient{
				retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
					return &referencedatav1.RetrieveInstrumentResponse{
						Instrument: &referencedatav1.InstrumentDefinition{
							Id:        uuid.New().String(),
							Code:      "TEST",
							Version:   1,
							Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY,
							Status:    tc.proto,
							CreatedAt: timestamppb.Now(),
						},
					}, nil
				},
			}

			source := NewGRPCSource(mock)
			ctx := testdb.ContextWithTenant(t, "tenant1")

			def, err := source.GetDefinition(ctx, "TEST", 1)
			require.NoError(t, err)
			assert.Equal(t, tc.domain, def.Status)
		})
	}
}

func TestGRPCSource_GetDefinition_NilTimestamps(t *testing.T) {
	mock := &mockGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Id:           uuid.New().String(),
					Code:         "USD",
					Version:      1,
					Dimension:    referencedatav1.Dimension_DIMENSION_CURRENCY,
					Status:       referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT,
					CreatedAt:    timestamppb.Now(),
					ActivatedAt:  nil,
					DeprecatedAt: nil,
					SuccessorId:  "",
				},
			}, nil
		},
	}

	source := NewGRPCSource(mock)
	ctx := testdb.ContextWithTenant(t, "tenant1")

	def, err := source.GetDefinition(ctx, "USD", 1)
	require.NoError(t, err)
	assert.Nil(t, def.ActivatedAt)
	assert.Nil(t, def.DeprecatedAt)
	assert.Nil(t, def.SuccessorID)
}

func TestGRPCSource_GetDefinition_NilInstrument(t *testing.T) {
	mock := &mockGRPCClient{
		retrieveFn: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: nil,
			}, nil
		},
	}

	source := NewGRPCSource(mock)
	ctx := testdb.ContextWithTenant(t, "tenant1")

	def, err := source.GetDefinition(ctx, "USD", 1)
	require.NoError(t, err)
	assert.Nil(t, def)
}
