package differ

import (
	"context"
	"testing"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise the gRPC adapter List* methods that wrap the generated
// clients. Each fake embeds the generated client interface so the embedded nil
// satisfies every method not overridden here; only the List RPC under test is
// implemented. The adapters live in the same package, so the fakes can be wired
// into the adapter structs directly via their unexported fields.

// ── Reference Data ──────────────────────────────────────────────────────────

type fakeReferenceDataServiceClient struct {
	referencedatav1.ReferenceDataServiceClient
	// responses are returned in order, one per ListInstruments call.
	responses []*referencedatav1.ListInstrumentsResponse
	err       error
	calls     []*referencedatav1.ListInstrumentsRequest
}

func (f *fakeReferenceDataServiceClient) ListInstruments(_ context.Context, in *referencedatav1.ListInstrumentsRequest, _ ...grpc.CallOption) (*referencedatav1.ListInstrumentsResponse, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.responses) {
		return &referencedatav1.ListInstrumentsResponse{}, nil
	}
	return f.responses[idx], nil
}

type fakeAccountTypeRegistryServiceClient struct {
	referencedatav1.AccountTypeRegistryServiceClient
	responses []*referencedatav1.ListAllResponse
	err       error
	calls     []*referencedatav1.ListAllRequest
}

func (f *fakeAccountTypeRegistryServiceClient) ListAll(_ context.Context, in *referencedatav1.ListAllRequest, _ ...grpc.CallOption) (*referencedatav1.ListAllResponse, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.responses) {
		return &referencedatav1.ListAllResponse{}, nil
	}
	return f.responses[idx], nil
}

func TestGRPCReferenceDataClient_ListInstruments(t *testing.T) {
	t.Run("maps and paginates", func(t *testing.T) {
		fake := &fakeReferenceDataServiceClient{
			responses: []*referencedatav1.ListInstrumentsResponse{
				{
					Instruments: []*referencedatav1.InstrumentDefinition{
						{Code: "GBP", DisplayName: "Pound", Dimension: referencedatav1.Dimension_DIMENSION_CURRENCY},
					},
					NextPageToken: "page2",
				},
				{
					Instruments: []*referencedatav1.InstrumentDefinition{
						{Code: "kWh", DisplayName: "Kilowatt Hour", Dimension: referencedatav1.Dimension_DIMENSION_ENERGY},
					},
				},
			},
		}
		c := &GRPCReferenceDataClient{instruments: fake}

		got, err := c.ListInstruments(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "GBP", got[0].GetCode())
		assert.Equal(t, "kWh", got[1].GetCode())
		// Second request must carry the token returned by the first page.
		require.Len(t, fake.calls, 2)
		assert.Equal(t, "page2", fake.calls[1].GetPageToken())
		assert.Equal(t, int32(defaultPageSize), fake.calls[0].GetPageSize())
	})

	t.Run("empty result", func(t *testing.T) {
		fake := &fakeReferenceDataServiceClient{
			responses: []*referencedatav1.ListInstrumentsResponse{{}},
		}
		c := &GRPCReferenceDataClient{instruments: fake}

		got, err := c.ListInstruments(context.Background())

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeReferenceDataServiceClient{err: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCReferenceDataClient{instruments: fake}

		got, err := c.ListInstruments(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeReferenceDataServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCReferenceDataClient{instruments: fake}

		_, err := c.ListInstruments(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list instruments")
	})
}

func TestGRPCReferenceDataClient_ListNonActiveInstrumentCodes(t *testing.T) {
	t.Run("collects only non-active codes across pages", func(t *testing.T) {
		fake := &fakeReferenceDataServiceClient{
			responses: []*referencedatav1.ListInstrumentsResponse{
				{
					Instruments: []*referencedatav1.InstrumentDefinition{
						{Code: "GBP", Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE},
						{Code: "OLD", Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED},
					},
					NextPageToken: "page2",
				},
				{
					Instruments: []*referencedatav1.InstrumentDefinition{
						{Code: "DRAFT_X", Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT},
					},
				},
			},
		}
		c := &GRPCReferenceDataClient{instruments: fake}

		got, err := c.ListNonActiveInstrumentCodes(context.Background())

		require.NoError(t, err)
		assert.Equal(t, map[string]bool{"OLD": true, "DRAFT_X": true}, got)
	})

	t.Run("empty-state error returns empty map", func(t *testing.T) {
		fake := &fakeReferenceDataServiceClient{err: status.Error(codes.Internal, "schema does not exist")}
		c := &GRPCReferenceDataClient{instruments: fake}

		got, err := c.ListNonActiveInstrumentCodes(context.Background())

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got, "should return an initialized (non-nil) map")
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeReferenceDataServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCReferenceDataClient{instruments: fake}

		_, err := c.ListNonActiveInstrumentCodes(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list non-active instruments")
	})
}

func TestGRPCReferenceDataClient_ListAccountTypes(t *testing.T) {
	t.Run("maps and paginates", func(t *testing.T) {
		fake := &fakeAccountTypeRegistryServiceClient{
			responses: []*referencedatav1.ListAllResponse{
				{
					Definitions: []*referencedatav1.AccountTypeDefinition{
						{Code: "CUSTOMER", DisplayName: "Customer"},
					},
					NextPageToken: "page2",
				},
				{
					Definitions: []*referencedatav1.AccountTypeDefinition{
						{Code: "CLEARING", DisplayName: "Clearing"},
					},
				},
			},
		}
		c := &GRPCReferenceDataClient{accountTypes: fake}

		got, err := c.ListAccountTypes(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "CUSTOMER", got[0].GetCode())
		assert.Equal(t, "CLEARING", got[1].GetCode())
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeAccountTypeRegistryServiceClient{err: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCReferenceDataClient{accountTypes: fake}

		got, err := c.ListAccountTypes(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeAccountTypeRegistryServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCReferenceDataClient{accountTypes: fake}

		_, err := c.ListAccountTypes(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list account types")
	})
}

// ── Saga Registry ───────────────────────────────────────────────────────────

type fakeSagaRegistryServiceClient struct {
	sagav1.SagaRegistryServiceClient
	responses []*sagav1.ListSagasResponse
	err       error
	calls     []*sagav1.ListSagasRequest
}

func (f *fakeSagaRegistryServiceClient) ListSagas(_ context.Context, in *sagav1.ListSagasRequest, _ ...grpc.CallOption) (*sagav1.ListSagasResponse, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.responses) {
		return &sagav1.ListSagasResponse{}, nil
	}
	return f.responses[idx], nil
}

func TestGRPCSagaRegistryClient_ListSagas(t *testing.T) {
	t.Run("maps and paginates", func(t *testing.T) {
		fake := &fakeSagaRegistryServiceClient{
			responses: []*sagav1.ListSagasResponse{
				{
					Sagas:         []*sagav1.SagaDefinition{{Name: "saga_a", Script: "a"}},
					NextPageToken: "page2",
				},
				{
					Sagas: []*sagav1.SagaDefinition{{Name: "saga_b", Script: "b"}},
				},
			},
		}
		c := &GRPCSagaRegistryClient{client: fake}

		got, err := c.ListSagas(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "saga_a", got[0].GetName())
		assert.Equal(t, "saga_b", got[1].GetName())
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeSagaRegistryServiceClient{err: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCSagaRegistryClient{client: fake}

		got, err := c.ListSagas(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeSagaRegistryServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCSagaRegistryClient{client: fake}

		_, err := c.ListSagas(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list sagas")
	})
}

// ── Market Information ──────────────────────────────────────────────────────

type fakeMarketInformationServiceClient struct {
	marketinformationv1.MarketInformationServiceClient
	sourceResponses []*marketinformationv1.ListDataSourcesResponse
	setResponses    []*marketinformationv1.ListDataSetsResponse
	sourceErr       error
	setErr          error
	sourceCalls     []*marketinformationv1.ListDataSourcesRequest
	setCalls        []*marketinformationv1.ListDataSetsRequest
}

func (f *fakeMarketInformationServiceClient) ListDataSources(_ context.Context, in *marketinformationv1.ListDataSourcesRequest, _ ...grpc.CallOption) (*marketinformationv1.ListDataSourcesResponse, error) {
	f.sourceCalls = append(f.sourceCalls, in)
	if f.sourceErr != nil {
		return nil, f.sourceErr
	}
	idx := len(f.sourceCalls) - 1
	if idx >= len(f.sourceResponses) {
		return &marketinformationv1.ListDataSourcesResponse{}, nil
	}
	return f.sourceResponses[idx], nil
}

func (f *fakeMarketInformationServiceClient) ListDataSets(_ context.Context, in *marketinformationv1.ListDataSetsRequest, _ ...grpc.CallOption) (*marketinformationv1.ListDataSetsResponse, error) {
	f.setCalls = append(f.setCalls, in)
	if f.setErr != nil {
		return nil, f.setErr
	}
	idx := len(f.setCalls) - 1
	if idx >= len(f.setResponses) {
		return &marketinformationv1.ListDataSetsResponse{}, nil
	}
	return f.setResponses[idx], nil
}

func TestGRPCMarketInformationClient_ListMarketDataSources(t *testing.T) {
	t.Run("maps and paginates", func(t *testing.T) {
		fake := &fakeMarketInformationServiceClient{
			sourceResponses: []*marketinformationv1.ListDataSourcesResponse{
				{
					Sources:       []*marketinformationv1.DataSource{{Code: "BLOOMBERG", Name: "Bloomberg"}},
					NextPageToken: "page2",
				},
				{
					Sources: []*marketinformationv1.DataSource{{Code: "REUTERS", Name: "Reuters"}},
				},
			},
		}
		c := &GRPCMarketInformationClient{client: fake}

		got, err := c.ListMarketDataSources(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "BLOOMBERG", got[0].GetCode())
		assert.Equal(t, "REUTERS", got[1].GetCode())
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeMarketInformationServiceClient{sourceErr: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCMarketInformationClient{client: fake}

		got, err := c.ListMarketDataSources(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeMarketInformationServiceClient{sourceErr: status.Error(codes.Internal, "boom")}
		c := &GRPCMarketInformationClient{client: fake}

		_, err := c.ListMarketDataSources(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list data sources")
	})
}

func TestGRPCMarketInformationClient_ListMarketDataSets(t *testing.T) {
	t.Run("maps and paginates", func(t *testing.T) {
		fake := &fakeMarketInformationServiceClient{
			setResponses: []*marketinformationv1.ListDataSetsResponse{
				{
					Datasets:      []*marketinformationv1.DataSetDefinition{{Code: "FX", Unit: "USD"}},
					NextPageToken: "page2",
				},
				{
					Datasets: []*marketinformationv1.DataSetDefinition{{Code: "PRICE", Unit: "GBP"}},
				},
			},
		}
		c := &GRPCMarketInformationClient{client: fake}

		got, err := c.ListMarketDataSets(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "FX", got[0].GetCode())
		assert.Equal(t, "PRICE", got[1].GetCode())
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeMarketInformationServiceClient{setErr: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCMarketInformationClient{client: fake}

		got, err := c.ListMarketDataSets(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeMarketInformationServiceClient{setErr: status.Error(codes.Internal, "boom")}
		c := &GRPCMarketInformationClient{client: fake}

		_, err := c.ListMarketDataSets(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list data sets")
	})
}

// ── Party ───────────────────────────────────────────────────────────────────

type fakePartyServiceClient struct {
	partyv1.PartyServiceClient
	responses []*partyv1.ListPartiesResponse
	err       error
	calls     []*partyv1.ListPartiesRequest
}

func (f *fakePartyServiceClient) ListParties(_ context.Context, in *partyv1.ListPartiesRequest, _ ...grpc.CallOption) (*partyv1.ListPartiesResponse, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.responses) {
		return &partyv1.ListPartiesResponse{}, nil
	}
	return f.responses[idx], nil
}

func TestGRPCPartyClient_ListOrganizations(t *testing.T) {
	t.Run("maps orgs, skips non-orgs, paginates", func(t *testing.T) {
		fake := &fakePartyServiceClient{
			responses: []*partyv1.ListPartiesResponse{
				{
					Parties: []*partyv1.Party{
						{PartyId: "ORG_A", PartyType: partyv1.PartyType_PARTY_TYPE_ORGANIZATION, LegalName: "Org A"},
						// Defensive filter: a non-org slips through, must be skipped.
						{PartyId: "PERSON_1", PartyType: partyv1.PartyType_PARTY_TYPE_PERSON, LegalName: "Person"},
					},
					NextPageToken: "page2",
				},
				{
					Parties: []*partyv1.Party{
						{PartyId: "ORG_B", PartyType: partyv1.PartyType_PARTY_TYPE_ORGANIZATION, LegalName: "Org B"},
					},
				},
			},
		}
		c := &GRPCPartyClient{client: fake}

		got, err := c.ListOrganizations(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "ORG_A", got[0].GetCode())
		assert.Equal(t, "ORG_B", got[1].GetCode())
		// Request must filter by organization party type.
		assert.Equal(t, partyv1.PartyType_PARTY_TYPE_ORGANIZATION, fake.calls[0].GetPartyType())
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakePartyServiceClient{err: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCPartyClient{client: fake}

		got, err := c.ListOrganizations(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakePartyServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCPartyClient{client: fake}

		_, err := c.ListOrganizations(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list organizations")
	})
}

// ── Internal Account ────────────────────────────────────────────────────────

type fakeInternalAccountServiceClient struct {
	internalaccountv1.InternalAccountServiceClient
	responses []*internalaccountv1.ListInternalAccountsResponse
	err       error
	calls     []*internalaccountv1.ListInternalAccountsRequest
}

func (f *fakeInternalAccountServiceClient) ListInternalAccounts(_ context.Context, in *internalaccountv1.ListInternalAccountsRequest, _ ...grpc.CallOption) (*internalaccountv1.ListInternalAccountsResponse, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.responses) {
		return &internalaccountv1.ListInternalAccountsResponse{}, nil
	}
	return f.responses[idx], nil
}

func TestGRPCInternalAccountClient_ListInternalAccounts(t *testing.T) {
	t.Run("maps and paginates via pagination response", func(t *testing.T) {
		fake := &fakeInternalAccountServiceClient{
			responses: []*internalaccountv1.ListInternalAccountsResponse{
				{
					Facilities: []*internalaccountv1.InternalAccountFacility{
						{AccountCode: "CLR-001", BehaviorClass: "CLEARING"},
					},
					Pagination: &commonv1.PaginationResponse{NextPageToken: "page2"},
				},
				{
					Facilities: []*internalaccountv1.InternalAccountFacility{
						{AccountCode: "CLR-002", BehaviorClass: "CLEARING"},
					},
					Pagination: &commonv1.PaginationResponse{NextPageToken: ""},
				},
			},
		}
		c := &GRPCInternalAccountClient{client: fake}

		got, err := c.ListInternalAccounts(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "CLR-001", got[0].GetCode())
		assert.Equal(t, "CLR-002", got[1].GetCode())
		require.Len(t, fake.calls, 2)
		assert.Equal(t, "page2", fake.calls[1].GetPagination().GetPageToken())
	})

	t.Run("nil pagination terminates loop", func(t *testing.T) {
		fake := &fakeInternalAccountServiceClient{
			responses: []*internalaccountv1.ListInternalAccountsResponse{
				{
					Facilities: []*internalaccountv1.InternalAccountFacility{
						{AccountCode: "ONLY"},
					},
					// No Pagination field -> nil -> single page.
				},
			},
		}
		c := &GRPCInternalAccountClient{client: fake}

		got, err := c.ListInternalAccounts(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Len(t, fake.calls, 1)
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeInternalAccountServiceClient{err: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCInternalAccountClient{client: fake}

		got, err := c.ListInternalAccounts(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeInternalAccountServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCInternalAccountClient{client: fake}

		_, err := c.ListInternalAccounts(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list internal accounts")
	})
}

// ── Operational Gateway ─────────────────────────────────────────────────────

type fakeProviderConnectionServiceClient struct {
	opgatewayv1.ProviderConnectionServiceClient
	responses []*opgatewayv1.ListConnectionsResponse
	err       error
	calls     []*opgatewayv1.ListConnectionsRequest
}

func (f *fakeProviderConnectionServiceClient) ListConnections(_ context.Context, in *opgatewayv1.ListConnectionsRequest, _ ...grpc.CallOption) (*opgatewayv1.ListConnectionsResponse, error) {
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	idx := len(f.calls) - 1
	if idx >= len(f.responses) {
		return &opgatewayv1.ListConnectionsResponse{}, nil
	}
	return f.responses[idx], nil
}

type fakeInstructionRouteServiceClient struct {
	opgatewayv1.InstructionRouteServiceClient
	response *opgatewayv1.ListRoutesResponse
	err      error
}

func (f *fakeInstructionRouteServiceClient) ListRoutes(_ context.Context, _ *opgatewayv1.ListRoutesRequest, _ ...grpc.CallOption) (*opgatewayv1.ListRoutesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func TestGRPCOperationalGatewayClient_ListProviderConnections(t *testing.T) {
	t.Run("maps and paginates", func(t *testing.T) {
		fake := &fakeProviderConnectionServiceClient{
			responses: []*opgatewayv1.ListConnectionsResponse{
				{
					Connections: []*opgatewayv1.ProviderConnection{{ConnectionId: "conn-1", ProviderName: "Stripe"}},
					Pagination:  &commonv1.PaginationResponse{NextPageToken: "page2"},
				},
				{
					Connections: []*opgatewayv1.ProviderConnection{{ConnectionId: "conn-2", ProviderName: "Plaid"}},
				},
			},
		}
		c := &GRPCOperationalGatewayClient{connClient: fake}

		got, err := c.ListProviderConnections(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "conn-1", got[0].GetConnectionId())
		assert.Equal(t, "conn-2", got[1].GetConnectionId())
		require.Len(t, fake.calls, 2)
		assert.Equal(t, "page2", fake.calls[1].GetPagination().GetPageToken())
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeProviderConnectionServiceClient{err: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCOperationalGatewayClient{connClient: fake}

		got, err := c.ListProviderConnections(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeProviderConnectionServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCOperationalGatewayClient{connClient: fake}

		_, err := c.ListProviderConnections(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list provider connections")
	})
}

func TestGRPCOperationalGatewayClient_ListInstructionRoutes(t *testing.T) {
	t.Run("maps routes (non-paginated)", func(t *testing.T) {
		fake := &fakeInstructionRouteServiceClient{
			response: &opgatewayv1.ListRoutesResponse{
				Routes: []*opgatewayv1.InstructionRoute{
					{InstructionType: "payment.initiate", ConnectionId: "conn-1"},
					{InstructionType: "payment.refund", ConnectionId: "conn-2"},
				},
			},
		}
		c := &GRPCOperationalGatewayClient{routeClient: fake}

		got, err := c.ListInstructionRoutes(context.Background())

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "payment.initiate", got[0].GetInstructionType())
		assert.Equal(t, "payment.refund", got[1].GetInstructionType())
	})

	t.Run("empty routes returns empty slice", func(t *testing.T) {
		fake := &fakeInstructionRouteServiceClient{
			response: &opgatewayv1.ListRoutesResponse{},
		}
		c := &GRPCOperationalGatewayClient{routeClient: fake}

		got, err := c.ListInstructionRoutes(context.Background())

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("empty-state error returns nil", func(t *testing.T) {
		fake := &fakeInstructionRouteServiceClient{err: status.Error(codes.Unimplemented, "no service")}
		c := &GRPCOperationalGatewayClient{routeClient: fake}

		got, err := c.ListInstructionRoutes(context.Background())

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("real error propagates", func(t *testing.T) {
		fake := &fakeInstructionRouteServiceClient{err: status.Error(codes.Internal, "boom")}
		c := &GRPCOperationalGatewayClient{routeClient: fake}

		_, err := c.ListInstructionRoutes(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "list instruction routes")
	})
}

// ── Constructors ────────────────────────────────────────────────────────────

// The constructors wire generated clients from a *grpc.ClientConn. A nil conn
// is sufficient to exercise the wiring: the generated NewXClient functions only
// store the connection, they do not dial. NewLiveStateClients additionally
// asserts the full provider wires up without error.
func TestConstructorsWireFromConn(t *testing.T) {
	var conn *grpc.ClientConn

	assert.NotNil(t, NewGRPCReferenceDataClient(conn))
	assert.NotNil(t, NewGRPCSagaRegistryClient(conn))
	assert.NotNil(t, NewGRPCMarketInformationClient(conn))
	assert.NotNil(t, NewGRPCPartyClient(conn))
	assert.NotNil(t, NewGRPCInternalAccountClient(conn))
	assert.NotNil(t, NewGRPCOperationalGatewayClient(conn))

	provider, err := NewLiveStateClients(conn)
	require.NoError(t, err)
	assert.NotNil(t, provider)
}
