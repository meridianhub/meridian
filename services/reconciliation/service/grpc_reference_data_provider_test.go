package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// --- Mock ReferenceDataServiceClient ---

type mockInstrumentClient struct {
	referencedatav1.ReferenceDataServiceClient
	instruments map[string]*referencedatav1.InstrumentDefinition
	err         error
}

func (m *mockInstrumentClient) RetrieveInstrument(_ context.Context, req *referencedatav1.RetrieveInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	inst, ok := m.instruments[req.GetCode()]
	if !ok {
		return nil, errors.New("instrument not found")
	}
	return &referencedatav1.RetrieveInstrumentResponse{Instrument: inst}, nil
}

// --- Mock AccountTypeRegistryServiceClient ---

type mockAccountTypeClient struct {
	referencedatav1.AccountTypeRegistryServiceClient
	definitions []*referencedatav1.AccountTypeDefinition
	err         error
}

func (m *mockAccountTypeClient) ListActive(_ context.Context, _ *referencedatav1.ListActiveRequest, _ ...grpc.CallOption) (*referencedatav1.ListActiveResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &referencedatav1.ListActiveResponse{Definitions: m.definitions}, nil
}

// --- Tests ---

func TestGRPCReferenceDataProvider_GetMaterialityThreshold_FromPrecision(t *testing.T) {
	instClient := &mockInstrumentClient{
		instruments: map[string]*referencedatav1.InstrumentDefinition{
			"GBP": {Code: "GBP", Precision: 2},
			"KWH": {Code: "KWH", Precision: 4},
		},
	}

	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		InstrumentClient: instClient,
		Logger:           valuationTestLogger(),
	})

	t.Run("GBP precision 2", func(t *testing.T) {
		threshold, err := provider.GetMaterialityThreshold(context.Background(), "GBP")
		require.NoError(t, err)
		assert.True(t, threshold.Equal(decimal.NewFromFloat(0.01)),
			"expected 0.01, got %s", threshold.String())
	})

	t.Run("KWH precision 4", func(t *testing.T) {
		threshold, err := provider.GetMaterialityThreshold(context.Background(), "KWH")
		require.NoError(t, err)
		assert.True(t, threshold.Equal(decimal.NewFromFloat(0.0001)),
			"expected 0.0001, got %s", threshold.String())
	})
}

func TestGRPCReferenceDataProvider_GetMaterialityThreshold_FallbackOnError(t *testing.T) {
	instClient := &mockInstrumentClient{err: errors.New("connection refused")}

	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		InstrumentClient: instClient,
		Logger:           valuationTestLogger(),
	})

	threshold, err := provider.GetMaterialityThreshold(context.Background(), "GBP")
	require.NoError(t, err)
	assert.True(t, threshold.Equal(decimal.NewFromFloat(0.01)),
		"should fall back to 0.01 on error")
}

func TestGRPCReferenceDataProvider_GetMaterialityThreshold_NoClient(t *testing.T) {
	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		Logger: valuationTestLogger(),
	})

	threshold, err := provider.GetMaterialityThreshold(context.Background(), "GBP")
	require.NoError(t, err)
	assert.True(t, threshold.Equal(decimal.NewFromFloat(0.01)))
}

func TestGRPCReferenceDataProvider_GetValuationMethodID_FromAccountType(t *testing.T) {
	methodID := uuid.New()
	acctClient := &mockAccountTypeClient{
		definitions: []*referencedatav1.AccountTypeDefinition{
			{
				Code: "CUSTOMER_CURRENT",
				ValuationMethods: []*referencedatav1.ValuationMethodTemplate{
					{
						InputInstrument:        "KWH",
						ValuationMethodId:      methodID.String(),
						ValuationMethodVersion: 1,
					},
				},
			},
		},
	}

	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		AccountTypeClient: acctClient,
		Logger:            testLogger(),
	})

	result, err := provider.GetValuationMethodID(context.Background(), "KWH")
	require.NoError(t, err)
	assert.Equal(t, methodID, result)
}

func TestGRPCReferenceDataProvider_GetValuationMethodID_FallbackToDefault(t *testing.T) {
	defaultID := uuid.New()
	acctClient := &mockAccountTypeClient{
		definitions: []*referencedatav1.AccountTypeDefinition{}, // No matching method
	}

	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		AccountTypeClient: acctClient,
		DefaultMethodID:   defaultID,
		Logger:            testLogger(),
	})

	result, err := provider.GetValuationMethodID(context.Background(), "UNKNOWN")
	require.NoError(t, err)
	assert.Equal(t, defaultID, result)
}

func TestGRPCReferenceDataProvider_GetValuationMethodID_NoClientUsesDefault(t *testing.T) {
	defaultID := uuid.New()

	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		DefaultMethodID: defaultID,
		Logger:          testLogger(),
	})

	result, err := provider.GetValuationMethodID(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, defaultID, result)
}

func TestGRPCReferenceDataProvider_GetValuationMethodID_NoDefaultNoClient_Error(t *testing.T) {
	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		Logger: valuationTestLogger(),
	})

	_, err := provider.GetValuationMethodID(context.Background(), "GBP")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoValuationMethod)
}

func TestGRPCReferenceDataProvider_GetValuationMethodID_AccountTypeError_FallsBackToDefault(t *testing.T) {
	defaultID := uuid.New()
	acctClient := &mockAccountTypeClient{err: errors.New("connection refused")}

	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		AccountTypeClient: acctClient,
		DefaultMethodID:   defaultID,
		Logger:            testLogger(),
	})

	result, err := provider.GetValuationMethodID(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, defaultID, result)
}

func TestGRPCReferenceDataProvider_GetValuationMethodID_DefaultConversionFallback(t *testing.T) {
	conversionID := uuid.New()
	acctClient := &mockAccountTypeClient{
		definitions: []*referencedatav1.AccountTypeDefinition{
			{
				Code:                      "CUSTOMER_CURRENT",
				DefaultConversionMethodId: conversionID.String(),
				// No ValuationMethods matching "EXOTIC" instrument
			},
		},
	}

	provider := NewGRPCReferenceDataProvider(GRPCReferenceDataProviderConfig{
		AccountTypeClient: acctClient,
		Logger:            testLogger(),
	})

	result, err := provider.GetValuationMethodID(context.Background(), "EXOTIC")
	require.NoError(t, err)
	assert.Equal(t, conversionID, result)
}
