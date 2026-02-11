package client

import (
	"context"
	"log/slog"
	"testing"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"
)

// mockPartyServiceClient implements partyv1.PartyServiceClient for testing.
type mockPartyServiceClient struct {
	partyv1.PartyServiceClient
	getDefaultPMResp *partyv1.GetDefaultPaymentMethodResponse
	getDefaultPMErr  error
}

func (m *mockPartyServiceClient) GetDefaultPaymentMethod(_ context.Context, _ *partyv1.GetDefaultPaymentMethodRequest, _ ...grpc.CallOption) (*partyv1.GetDefaultPaymentMethodResponse, error) {
	return m.getDefaultPMResp, m.getDefaultPMErr
}

func newTestStarlarkContext() *saga.StarlarkContext {
	return &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		Logger:          slog.Default(),
	}
}

func TestRegisterStarlarkHandlers(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	client := &Client{party: &mockPartyServiceClient{}}

	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	assert.True(t, registry.Has("party.get_default_payment_method"))
}

func TestRegisterStarlarkHandlers_DuplicateRegistration(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	client := &Client{party: &mockPartyServiceClient{}}

	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	err = RegisterStarlarkHandlers(registry, client)
	assert.ErrorIs(t, err, saga.ErrHandlerAlreadyRegistered)
}

func TestGetDefaultPaymentMethodHandler_Success(t *testing.T) {
	mock := &mockPartyServiceClient{
		getDefaultPMResp: &partyv1.GetDefaultPaymentMethodResponse{
			PaymentMethod: &partyv1.PartyPaymentMethod{
				Provider:           partyv1.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
				ProviderCustomerId: "cus_test123456",
				ProviderMethodId:   "pm_test789012",
				MethodType:         partyv1.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
			},
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_default_payment_method")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"party_id": uuid.New().String(),
	})

	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "PAYMENT_METHOD_PROVIDER_STRIPE", resultMap["provider"])
	assert.Equal(t, "cus_test123456", resultMap["provider_customer_id"])
	assert.Equal(t, "pm_test789012", resultMap["provider_method_id"])
	assert.Equal(t, "PAYMENT_METHOD_TYPE_CARD", resultMap["method_type"])
}

func TestGetDefaultPaymentMethodHandler_MissingPartyID(t *testing.T) {
	client := &Client{party: &mockPartyServiceClient{}}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_default_payment_method")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{})

	assert.ErrorIs(t, err, saga.ErrMissingParam)
}

func TestGetDefaultPaymentMethodHandler_NilPaymentMethod(t *testing.T) {
	mock := &mockPartyServiceClient{
		getDefaultPMResp: &partyv1.GetDefaultPaymentMethodResponse{
			PaymentMethod: nil,
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_default_payment_method")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"party_id": uuid.New().String(),
	})

	assert.ErrorIs(t, err, errMissingPaymentMethod)
}

func TestGetDefaultPaymentMethodHandler_NotFound(t *testing.T) {
	mock := &mockPartyServiceClient{
		getDefaultPMErr: status.Errorf(codes.NotFound, "no default payment method"),
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_default_payment_method")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"party_id": uuid.New().String(),
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "party.get_default_payment_method")
}
