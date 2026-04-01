package differ

import (
	"context"
	"errors"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock clients ---

type mockReferenceDataClient struct {
	instruments  []*controlplanev1.InstrumentDefinition
	accountTypes []*controlplanev1.AccountTypeDefinition
	instrErr     error
	acctErr      error
}

func (m *mockReferenceDataClient) ListInstruments(_ context.Context) ([]*controlplanev1.InstrumentDefinition, error) {
	return m.instruments, m.instrErr
}

func (m *mockReferenceDataClient) ListAccountTypes(_ context.Context) ([]*controlplanev1.AccountTypeDefinition, error) {
	return m.accountTypes, m.acctErr
}

type mockSagaRegistryClient struct {
	sagas []*controlplanev1.SagaDefinition
	err   error
}

func (m *mockSagaRegistryClient) ListSagas(_ context.Context) ([]*controlplanev1.SagaDefinition, error) {
	return m.sagas, m.err
}

type mockMarketInformationClient struct {
	sources  []*controlplanev1.MarketDataSourceDefinition
	datasets []*controlplanev1.MarketDataSetDefinition
	srcErr   error
	dsErr    error
}

func (m *mockMarketInformationClient) ListMarketDataSources(_ context.Context) ([]*controlplanev1.MarketDataSourceDefinition, error) {
	return m.sources, m.srcErr
}

func (m *mockMarketInformationClient) ListMarketDataSets(_ context.Context) ([]*controlplanev1.MarketDataSetDefinition, error) {
	return m.datasets, m.dsErr
}

type mockPartyClient struct {
	organizations []*controlplanev1.OrganizationDefinition
	err           error
}

func (m *mockPartyClient) ListOrganizations(_ context.Context) ([]*controlplanev1.OrganizationDefinition, error) {
	return m.organizations, m.err
}

type mockInternalAccountClient struct {
	accounts []*controlplanev1.InternalAccountDefinition
	err      error
}

func (m *mockInternalAccountClient) ListInternalAccounts(_ context.Context) ([]*controlplanev1.InternalAccountDefinition, error) {
	return m.accounts, m.err
}

type mockOperationalGatewayClient struct {
	connections []*controlplanev1.ProviderConnectionConfig
	routes      []*controlplanev1.InstructionRouteConfig
	connErr     error
	routeErr    error
}

func (m *mockOperationalGatewayClient) ListProviderConnections(_ context.Context) ([]*controlplanev1.ProviderConnectionConfig, error) {
	return m.connections, m.connErr
}

func (m *mockOperationalGatewayClient) ListInstructionRoutes(_ context.Context) ([]*controlplanev1.InstructionRouteConfig, error) {
	return m.routes, m.routeErr
}

// --- Helper to build a provider with all healthy mocks ---

func newTestMocks() (
	*mockReferenceDataClient,
	*mockSagaRegistryClient,
	*mockMarketInformationClient,
	*mockPartyClient,
	*mockInternalAccountClient,
	*mockOperationalGatewayClient,
) {
	return &mockReferenceDataClient{
			instruments: []*controlplanev1.InstrumentDefinition{
				{Code: "GBP", Name: "British Pound"},
				{Code: "KWH", Name: "Kilowatt Hour"},
			},
			accountTypes: []*controlplanev1.AccountTypeDefinition{
				{Code: "CURRENT", Name: "Current Account"},
			},
		},
		&mockSagaRegistryClient{
			sagas: []*controlplanev1.SagaDefinition{
				{Name: "process_payment"},
			},
		},
		&mockMarketInformationClient{
			sources: []*controlplanev1.MarketDataSourceDefinition{
				{Code: "ECB"},
			},
			datasets: []*controlplanev1.MarketDataSetDefinition{
				{Code: "FX_RATES"},
			},
		},
		&mockPartyClient{
			organizations: []*controlplanev1.OrganizationDefinition{
				{Code: "ACME"},
			},
		},
		&mockInternalAccountClient{
			accounts: []*controlplanev1.InternalAccountDefinition{
				{Code: "SUSPENSE_GBP"},
			},
		},
		&mockOperationalGatewayClient{
			connections: []*controlplanev1.ProviderConnectionConfig{
				{ConnectionId: "stripe-prod", ProviderName: "Stripe Production"},
			},
			routes: []*controlplanev1.InstructionRouteConfig{
				{InstructionType: "PAYMENT", ConnectionId: "stripe-prod"},
			},
		}
}

func newTestProvider(t *testing.T) (
	*GRPCLiveStateProvider,
	*mockReferenceDataClient,
	*mockSagaRegistryClient,
	*mockMarketInformationClient,
	*mockPartyClient,
	*mockInternalAccountClient,
	*mockOperationalGatewayClient,
) {
	t.Helper()
	refData, saga, market, party, intAcct, opGw := newTestMocks()
	provider, err := NewGRPCLiveStateProvider(refData, saga, market, party, intAcct, opGw)
	require.NoError(t, err)
	return provider, refData, saga, market, party, intAcct, opGw
}

// --- Constructor tests ---

func TestNewGRPCLiveStateProvider_AllClientsRequired(t *testing.T) {
	refData, saga, market, party, intAcct, opGw := newTestMocks()

	tests := []struct {
		name           string
		refData        ReferenceDataClient
		saga           SagaRegistryClient
		market         MarketInformationClient
		party          PartyClient
		intAcct        InternalAccountClient
		opGw           OperationalGatewayClient
		expectContains string
	}{
		{"nil referenceData", nil, saga, market, party, intAcct, opGw, "referenceData"},
		{"nil sagaRegistry", refData, nil, market, party, intAcct, opGw, "sagaRegistry"},
		{"nil marketInformation", refData, saga, nil, party, intAcct, opGw, "marketInformation"},
		{"nil party", refData, saga, market, nil, intAcct, opGw, "party"},
		{"nil internalAccount", refData, saga, market, party, nil, opGw, "internalAccount"},
		{"nil operationalGateway", refData, saga, market, party, intAcct, nil, "operationalGateway"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewGRPCLiveStateProvider(tt.refData, tt.saga, tt.market, tt.party, tt.intAcct, tt.opGw)
			assert.Nil(t, provider)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectContains)
		})
	}
}

func TestNewGRPCLiveStateProvider_Success(t *testing.T) {
	refData, saga, market, party, intAcct, opGw := newTestMocks()
	provider, err := NewGRPCLiveStateProvider(refData, saga, market, party, intAcct, opGw)
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

// --- QueryLiveState tests ---

func TestQueryLiveState_AllServicesSucceed(t *testing.T) {
	provider, _, _, _, _, _, _ := newTestProvider(t)

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Len(t, state.Instruments, 2)
	assert.Equal(t, "GBP", state.Instruments[0].Code)
	assert.Equal(t, "KWH", state.Instruments[1].Code)

	assert.Len(t, state.AccountTypes, 1)
	assert.Equal(t, "CURRENT", state.AccountTypes[0].Code)

	assert.Len(t, state.Sagas, 1)
	assert.Equal(t, "process_payment", state.Sagas[0].Name)

	assert.Len(t, state.MarketDataSources, 1)
	assert.Equal(t, "ECB", state.MarketDataSources[0].Code)

	assert.Len(t, state.MarketDataSets, 1)
	assert.Equal(t, "FX_RATES", state.MarketDataSets[0].Code)

	assert.Len(t, state.Organizations, 1)
	assert.Equal(t, "ACME", state.Organizations[0].Code)

	assert.Len(t, state.InternalAccounts, 1)
	assert.Equal(t, "SUSPENSE_GBP", state.InternalAccounts[0].Code)

	assert.Len(t, state.ProviderConnections, 1)
	assert.Equal(t, "stripe-prod", state.ProviderConnections[0].ConnectionId)

	assert.Len(t, state.InstructionRoutes, 1)
	assert.Equal(t, "PAYMENT", state.InstructionRoutes[0].InstructionType)
}

func TestQueryLiveState_EmptyResults(t *testing.T) {
	refData, saga, market, party, intAcct, opGw := newTestMocks()
	// Override all with empty slices
	refData.instruments = nil
	refData.accountTypes = nil
	saga.sagas = nil
	market.sources = nil
	market.datasets = nil
	party.organizations = nil
	intAcct.accounts = nil
	opGw.connections = nil
	opGw.routes = nil

	provider, err := NewGRPCLiveStateProvider(refData, saga, market, party, intAcct, opGw)
	require.NoError(t, err)

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Empty(t, state.Instruments)
	assert.Empty(t, state.AccountTypes)
	assert.Empty(t, state.Sagas)
	assert.Empty(t, state.MarketDataSources)
	assert.Empty(t, state.MarketDataSets)
	assert.Empty(t, state.Organizations)
	assert.Empty(t, state.InternalAccounts)
	assert.Empty(t, state.ProviderConnections)
	assert.Empty(t, state.InstructionRoutes)
}

func TestQueryLiveState_InstrumentQueryFails(t *testing.T) {
	provider, refData, _, _, _, _, _ := newTestProvider(t)
	refData.instrErr = errors.New("connection refused")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query instruments")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestQueryLiveState_AccountTypeQueryFails(t *testing.T) {
	provider, refData, _, _, _, _, _ := newTestProvider(t)
	refData.acctErr = errors.New("timeout")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query account types")
}

func TestQueryLiveState_SagaQueryFails(t *testing.T) {
	provider, _, saga, _, _, _, _ := newTestProvider(t)
	saga.err = errors.New("unavailable")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query sagas")
}

func TestQueryLiveState_MarketDataSourceQueryFails(t *testing.T) {
	provider, _, _, market, _, _, _ := newTestProvider(t)
	market.srcErr = errors.New("not found")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query market data sources")
}

func TestQueryLiveState_MarketDataSetQueryFails(t *testing.T) {
	provider, _, _, market, _, _, _ := newTestProvider(t)
	market.dsErr = errors.New("internal error")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query market data sets")
}

func TestQueryLiveState_OrganizationQueryFails(t *testing.T) {
	provider, _, _, _, party, _, _ := newTestProvider(t)
	party.err = errors.New("permission denied")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query organizations")
}

func TestQueryLiveState_InternalAccountQueryFails(t *testing.T) {
	provider, _, _, _, _, intAcct, _ := newTestProvider(t)
	intAcct.err = errors.New("deadline exceeded")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query internal accounts")
}

func TestQueryLiveState_ProviderConnectionQueryFails(t *testing.T) {
	provider, _, _, _, _, _, opGw := newTestProvider(t)
	opGw.connErr = errors.New("connection reset")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query provider connections")
}

func TestQueryLiveState_InstructionRouteQueryFails(t *testing.T) {
	provider, _, _, _, _, _, opGw := newTestProvider(t)
	opGw.routeErr = errors.New("service unavailable")

	state, err := provider.QueryLiveState(context.Background(), "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query instruction routes")
}

func TestQueryLiveState_ContextCancelled(t *testing.T) {
	// Use a client that respects context cancellation to verify errgroup propagation.
	refData := &contextAwareRefDataClient{}
	saga := &mockSagaRegistryClient{sagas: []*controlplanev1.SagaDefinition{{Name: "s"}}}
	market := &mockMarketInformationClient{}
	party := &mockPartyClient{}
	intAcct := &mockInternalAccountClient{}
	opGw := &mockOperationalGatewayClient{}

	provider, err := NewGRPCLiveStateProvider(refData, saga, market, party, intAcct, opGw)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	state, err := provider.QueryLiveState(ctx, "tenant-1")
	assert.Nil(t, state)
	require.Error(t, err)
}

// contextAwareRefDataClient checks context before returning.
type contextAwareRefDataClient struct{}

func (c *contextAwareRefDataClient) ListInstruments(ctx context.Context) ([]*controlplanev1.InstrumentDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (c *contextAwareRefDataClient) ListAccountTypes(ctx context.Context) ([]*controlplanev1.AccountTypeDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func TestQueryLiveState_ImplementsLiveStateProvider(t *testing.T) {
	provider, _, _, _, _, _, _ := newTestProvider(t)
	// Compile-time check that GRPCLiveStateProvider implements LiveStateProvider.
	var _ LiveStateProvider = provider
}
