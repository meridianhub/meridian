package differ

import (
	"context"
	"fmt"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"golang.org/x/sync/errgroup"
)

// Service-specific client interfaces, decoupled from generated gRPC stubs.
// Each returns manifest-compatible control-plane proto types.

// ReferenceDataClient queries the reference-data service for instruments and account types.
type ReferenceDataClient interface {
	ListInstruments(ctx context.Context) ([]*controlplanev1.InstrumentDefinition, error)
	ListAccountTypes(ctx context.Context) ([]*controlplanev1.AccountTypeDefinition, error)
}

// SagaRegistryClient queries the saga-registry service for saga definitions.
type SagaRegistryClient interface {
	ListSagas(ctx context.Context) ([]*controlplanev1.SagaDefinition, error)
}

// MarketInformationClient queries the market-information service for data sources and data sets.
type MarketInformationClient interface {
	ListMarketDataSources(ctx context.Context) ([]*controlplanev1.MarketDataSourceDefinition, error)
	ListMarketDataSets(ctx context.Context) ([]*controlplanev1.MarketDataSetDefinition, error)
}

// PartyClient queries the party service for organizations.
type PartyClient interface {
	ListOrganizations(ctx context.Context) ([]*controlplanev1.OrganizationDefinition, error)
}

// InternalAccountClient queries the internal-account service for internal accounts.
type InternalAccountClient interface {
	ListInternalAccounts(ctx context.Context) ([]*controlplanev1.InternalAccountDefinition, error)
}

// OperationalGatewayClient queries the operational-gateway service for provider connections and instruction routes.
type OperationalGatewayClient interface {
	ListProviderConnections(ctx context.Context) ([]*controlplanev1.ProviderConnectionConfig, error)
	ListInstructionRoutes(ctx context.Context) ([]*controlplanev1.InstructionRouteConfig, error)
}

// GRPCLiveStateProvider queries all downstream services via gRPC to build a LiveState snapshot.
// All service queries run concurrently via errgroup. If any query fails, the entire
// operation fails - partial state is not acceptable for diff correctness.
type GRPCLiveStateProvider struct {
	referenceData      ReferenceDataClient
	sagaRegistry       SagaRegistryClient
	marketInformation  MarketInformationClient
	party              PartyClient
	internalAccount    InternalAccountClient
	operationalGateway OperationalGatewayClient
}

// NewGRPCLiveStateProvider creates a new GRPCLiveStateProvider with all required service clients.
// All client parameters are required and must not be nil.
func NewGRPCLiveStateProvider(
	referenceData ReferenceDataClient,
	sagaRegistry SagaRegistryClient,
	marketInformation MarketInformationClient,
	party PartyClient,
	internalAccount InternalAccountClient,
	operationalGateway OperationalGatewayClient,
) (*GRPCLiveStateProvider, error) {
	if referenceData == nil {
		return nil, fmt.Errorf("referenceData client is required")
	}
	if sagaRegistry == nil {
		return nil, fmt.Errorf("sagaRegistry client is required")
	}
	if marketInformation == nil {
		return nil, fmt.Errorf("marketInformation client is required")
	}
	if party == nil {
		return nil, fmt.Errorf("party client is required")
	}
	if internalAccount == nil {
		return nil, fmt.Errorf("internalAccount client is required")
	}
	if operationalGateway == nil {
		return nil, fmt.Errorf("operationalGateway client is required")
	}
	return &GRPCLiveStateProvider{
		referenceData:      referenceData,
		sagaRegistry:       sagaRegistry,
		marketInformation:  marketInformation,
		party:              party,
		internalAccount:    internalAccount,
		operationalGateway: operationalGateway,
	}, nil
}

// QueryLiveState queries all downstream services concurrently and returns the aggregated live state.
// The tenantID parameter is passed through context by the caller (tenant middleware).
// Returns an error if any service query fails.
func (p *GRPCLiveStateProvider) QueryLiveState(ctx context.Context, _ string) (*LiveState, error) {
	state := &LiveState{}
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		instruments, err := p.referenceData.ListInstruments(gctx)
		if err != nil {
			return fmt.Errorf("query instruments: %w", err)
		}
		state.Instruments = instruments
		return nil
	})

	g.Go(func() error {
		accountTypes, err := p.referenceData.ListAccountTypes(gctx)
		if err != nil {
			return fmt.Errorf("query account types: %w", err)
		}
		state.AccountTypes = accountTypes
		return nil
	})

	g.Go(func() error {
		sagas, err := p.sagaRegistry.ListSagas(gctx)
		if err != nil {
			return fmt.Errorf("query sagas: %w", err)
		}
		state.Sagas = sagas
		return nil
	})

	g.Go(func() error {
		sources, err := p.marketInformation.ListMarketDataSources(gctx)
		if err != nil {
			return fmt.Errorf("query market data sources: %w", err)
		}
		state.MarketDataSources = sources
		return nil
	})

	g.Go(func() error {
		datasets, err := p.marketInformation.ListMarketDataSets(gctx)
		if err != nil {
			return fmt.Errorf("query market data sets: %w", err)
		}
		state.MarketDataSets = datasets
		return nil
	})

	g.Go(func() error {
		orgs, err := p.party.ListOrganizations(gctx)
		if err != nil {
			return fmt.Errorf("query organizations: %w", err)
		}
		state.Organizations = orgs
		return nil
	})

	g.Go(func() error {
		accounts, err := p.internalAccount.ListInternalAccounts(gctx)
		if err != nil {
			return fmt.Errorf("query internal accounts: %w", err)
		}
		state.InternalAccounts = accounts
		return nil
	})

	g.Go(func() error {
		connections, err := p.operationalGateway.ListProviderConnections(gctx)
		if err != nil {
			return fmt.Errorf("query provider connections: %w", err)
		}
		state.ProviderConnections = connections
		return nil
	})

	g.Go(func() error {
		routes, err := p.operationalGateway.ListInstructionRoutes(gctx)
		if err != nil {
			return fmt.Errorf("query instruction routes: %w", err)
		}
		state.InstructionRoutes = routes
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("query live state: %w", err)
	}

	return state, nil
}
