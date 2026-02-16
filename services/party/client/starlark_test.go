package client

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// mockPartyServiceClient implements partyv1.PartyServiceClient for testing.
type mockPartyServiceClient struct {
	partyv1.PartyServiceClient
	getDefaultPMResp      *partyv1.GetDefaultPaymentMethodResponse
	getDefaultPMErr       error
	listParticipants      *partyv1.ListParticipantsResponse
	listParticipantsErr   error
	getStructuringData    *partyv1.GetStructuringDataResponse
	getStructuringDataErr error
}

func (m *mockPartyServiceClient) GetDefaultPaymentMethod(_ context.Context, _ *partyv1.GetDefaultPaymentMethodRequest, _ ...grpc.CallOption) (*partyv1.GetDefaultPaymentMethodResponse, error) {
	return m.getDefaultPMResp, m.getDefaultPMErr
}

func (m *mockPartyServiceClient) ListParticipants(_ context.Context, _ *partyv1.ListParticipantsRequest, _ ...grpc.CallOption) (*partyv1.ListParticipantsResponse, error) {
	return m.listParticipants, m.listParticipantsErr
}

func (m *mockPartyServiceClient) GetStructuringData(_ context.Context, _ *partyv1.GetStructuringDataRequest, _ ...grpc.CallOption) (*partyv1.GetStructuringDataResponse, error) {
	return m.getStructuringData, m.getStructuringDataErr
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
	assert.True(t, registry.Has("party.list_participants"))
	assert.True(t, registry.Has("party.get_structuring_data"))
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

func TestGetDefaultPaymentMethodHandler_PartyScopeViolation(t *testing.T) {
	client := &Client{party: &mockPartyServiceClient{}}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_default_payment_method")
	require.NoError(t, err)

	ownPartyID := uuid.New()
	foreignPartyID := uuid.New()

	ctx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		Logger:          slog.Default(),
		PartyScope: &saga.PartyScope{
			PartyID:        ownPartyID,
			VisibleParties: []uuid.UUID{ownPartyID},
		},
	}

	_, err = handler(ctx, map[string]any{
		"party_id": foreignPartyID.String(),
	})

	assert.ErrorIs(t, err, saga.ErrPartyScopeViolation)
}

// --- ListParticipants handler tests ---

func TestListParticipantsHandler_Success(t *testing.T) {
	metadata, err := structpb.NewStruct(map[string]interface{}{
		"allocation_share": 0.25,
		"role":             "participant",
	})
	require.NoError(t, err)

	participantID := uuid.New().String()
	mock := &mockPartyServiceClient{
		listParticipants: &partyv1.ListParticipantsResponse{
			Participants: []*partyv1.Association{
				{
					RelatedPartyId: participantID,
					Metadata:       metadata,
				},
			},
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err = RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.list_participants")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"org_id":            uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})

	require.NoError(t, err)
	resultList, ok := result.([]any)
	require.True(t, ok)
	require.Len(t, resultList, 1)

	entry, ok := resultList[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, participantID, entry["party_id"])

	md, ok := entry["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 0.25, md["allocation_share"])
	assert.Equal(t, "participant", md["role"])
}

func TestListParticipantsHandler_EmptyList(t *testing.T) {
	mock := &mockPartyServiceClient{
		listParticipants: &partyv1.ListParticipantsResponse{
			Participants: []*partyv1.Association{},
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.list_participants")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"org_id":            uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})

	require.NoError(t, err)
	resultList, ok := result.([]any)
	require.True(t, ok)
	assert.Empty(t, resultList)
}

func TestListParticipantsHandler_MissingOrgID(t *testing.T) {
	client := &Client{party: &mockPartyServiceClient{}}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.list_participants")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})
	assert.ErrorIs(t, err, saga.ErrMissingParam)
}

func TestListParticipantsHandler_MissingRelationshipType(t *testing.T) {
	client := &Client{party: &mockPartyServiceClient{}}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.list_participants")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"org_id": uuid.New().String(),
	})
	assert.ErrorIs(t, err, saga.ErrMissingParam)
}

func TestListParticipantsHandler_InvalidRelationshipType(t *testing.T) {
	client := &Client{party: &mockPartyServiceClient{}}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.list_participants")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"org_id":            uuid.New().String(),
		"relationship_type": "INVALID_TYPE",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid relationship_type")
}

func TestListParticipantsHandler_GRPCError(t *testing.T) {
	mock := &mockPartyServiceClient{
		listParticipantsErr: status.Errorf(codes.NotFound, "syndicate not found"),
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.list_participants")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"org_id":            uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "party.list_participants")
}

func TestListParticipantsHandler_CacheHit(t *testing.T) {
	// First call returns real data
	participantID := uuid.New().String()
	mock := &mockPartyServiceClient{
		listParticipants: &partyv1.ListParticipantsResponse{
			Participants: []*partyv1.Association{
				{RelatedPartyId: participantID},
			},
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.list_participants")
	require.NoError(t, err)

	orgID := uuid.New().String()
	ctx := newTestStarlarkContext()
	ctx.LookupCache = saga.NewLookupResultCache()

	params := map[string]any{
		"org_id":            orgID,
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	}

	// First call populates cache
	result1, err := handler(ctx, params)
	require.NoError(t, err)

	// Make mock return error - second call should use cache
	mock.listParticipantsErr = status.Errorf(codes.Internal, "should not be called")
	mock.listParticipants = nil

	result2, err := handler(ctx, params)
	require.NoError(t, err)

	// Both should return same data
	list1, ok := result1.([]any)
	require.True(t, ok)
	list2, ok := result2.([]any)
	require.True(t, ok)
	assert.Equal(t, len(list1), len(list2))
}

// --- GetStructuringData handler tests ---

func TestGetStructuringDataHandler_Success(t *testing.T) {
	metadata, err := structpb.NewStruct(map[string]interface{}{
		"allocation_share": 0.5,
		"role":             "lead",
	})
	require.NoError(t, err)

	mock := &mockPartyServiceClient{
		getStructuringData: &partyv1.GetStructuringDataResponse{
			Metadata: metadata,
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err = RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_structuring_data")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"party_id":          uuid.New().String(),
		"org_id":            uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})

	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 0.5, resultMap["allocation_share"])
	assert.Equal(t, "lead", resultMap["role"])
}

func TestGetStructuringDataHandler_EmptyMetadata(t *testing.T) {
	mock := &mockPartyServiceClient{
		getStructuringData: &partyv1.GetStructuringDataResponse{
			Metadata: nil,
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_structuring_data")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	result, err := handler(ctx, map[string]any{
		"party_id":          uuid.New().String(),
		"org_id":            uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})

	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Empty(t, resultMap)
}

func TestGetStructuringDataHandler_MissingPartyID(t *testing.T) {
	client := &Client{party: &mockPartyServiceClient{}}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_structuring_data")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"org_id":            uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})
	assert.ErrorIs(t, err, saga.ErrMissingParam)
}

func TestGetStructuringDataHandler_MissingOrgID(t *testing.T) {
	client := &Client{party: &mockPartyServiceClient{}}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_structuring_data")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"party_id":          uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})
	assert.ErrorIs(t, err, saga.ErrMissingParam)
}

func TestGetStructuringDataHandler_GRPCError(t *testing.T) {
	mock := &mockPartyServiceClient{
		getStructuringDataErr: status.Errorf(codes.NotFound, "structuring data not found"),
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_structuring_data")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	_, err = handler(ctx, map[string]any{
		"party_id":          uuid.New().String(),
		"org_id":            uuid.New().String(),
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "party.get_structuring_data")
}

func TestGetStructuringDataHandler_CacheHit(t *testing.T) {
	metadata, err := structpb.NewStruct(map[string]interface{}{
		"allocation_share": 0.75,
	})
	require.NoError(t, err)

	mock := &mockPartyServiceClient{
		getStructuringData: &partyv1.GetStructuringDataResponse{
			Metadata: metadata,
		},
	}

	client := &Client{party: mock}
	registry := saga.NewHandlerRegistry()
	err = RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	handler, err := registry.Get("party.get_structuring_data")
	require.NoError(t, err)

	partyID := uuid.New().String()
	orgID := uuid.New().String()
	ctx := newTestStarlarkContext()
	ctx.LookupCache = saga.NewLookupResultCache()

	params := map[string]any{
		"party_id":          partyID,
		"org_id":            orgID,
		"relationship_type": "RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT",
	}

	// First call populates cache
	result1, err := handler(ctx, params)
	require.NoError(t, err)

	// Make mock return error - second call should use cache
	mock.getStructuringDataErr = status.Errorf(codes.Internal, "should not be called")
	mock.getStructuringData = nil

	result2, err := handler(ctx, params)
	require.NoError(t, err)

	// Both should return same data
	map1, ok := result1.(map[string]any)
	require.True(t, ok)
	map2, ok := result2.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map1["allocation_share"], map2["allocation_share"])
}
