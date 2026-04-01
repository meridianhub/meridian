package differ

import (
	"context"
	"errors"
	"fmt"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"
)

// Sentinel errors for nil client parameters.
var (
	ErrNilReferenceDataClient      = errors.New("referenceData client is required")
	ErrNilSagaRegistryClient       = errors.New("sagaRegistry client is required")
	ErrNilMarketInformationClient  = errors.New("marketInformation client is required")
	ErrNilPartyClient              = errors.New("party client is required")
	ErrNilInternalAccountClient    = errors.New("internalAccount client is required")
	ErrNilOperationalGatewayClient = errors.New("operationalGateway client is required")
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
		return nil, ErrNilReferenceDataClient
	}
	if sagaRegistry == nil {
		return nil, ErrNilSagaRegistryClient
	}
	if marketInformation == nil {
		return nil, ErrNilMarketInformationClient
	}
	if party == nil {
		return nil, ErrNilPartyClient
	}
	if internalAccount == nil {
		return nil, ErrNilInternalAccountClient
	}
	if operationalGateway == nil {
		return nil, ErrNilOperationalGatewayClient
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
// tenantID is propagated via gRPC metadata so downstream services scope queries to the correct tenant.
// Returns an error if any service query fails.
func (p *GRPCLiveStateProvider) QueryLiveState(ctx context.Context, tenantID string) (*LiveState, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(tenant.TenantIDKey, tenantID)
	ctx = metadata.NewOutgoingContext(ctx, md)

	state := &LiveState{}
	g, gctx := errgroup.WithContext(ctx)

	p.scheduleReferenceDataQueries(gctx, g, state)
	p.scheduleSagaQuery(gctx, g, state)
	p.scheduleMarketDataQueries(gctx, g, state)
	p.schedulePartyQuery(gctx, g, state)
	p.scheduleInternalAccountQuery(gctx, g, state)
	p.scheduleOperationalGatewayQueries(gctx, g, state)

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("query live state: %w", err)
	}
	return state, nil
}

func (p *GRPCLiveStateProvider) scheduleReferenceDataQueries(ctx context.Context, g *errgroup.Group, state *LiveState) {
	g.Go(func() error {
		instruments, err := p.referenceData.ListInstruments(ctx)
		if err != nil {
			return fmt.Errorf("query instruments: %w", err)
		}
		state.Instruments = instruments
		return nil
	})
	g.Go(func() error {
		accountTypes, err := p.referenceData.ListAccountTypes(ctx)
		if err != nil {
			return fmt.Errorf("query account types: %w", err)
		}
		state.AccountTypes = accountTypes
		return nil
	})
}

func (p *GRPCLiveStateProvider) scheduleSagaQuery(ctx context.Context, g *errgroup.Group, state *LiveState) {
	g.Go(func() error {
		sagas, err := p.sagaRegistry.ListSagas(ctx)
		if err != nil {
			return fmt.Errorf("query sagas: %w", err)
		}
		state.Sagas = sagas
		return nil
	})
}

func (p *GRPCLiveStateProvider) scheduleMarketDataQueries(ctx context.Context, g *errgroup.Group, state *LiveState) {
	g.Go(func() error {
		sources, err := p.marketInformation.ListMarketDataSources(ctx)
		if err != nil {
			return fmt.Errorf("query market data sources: %w", err)
		}
		state.MarketDataSources = sources
		return nil
	})
	g.Go(func() error {
		datasets, err := p.marketInformation.ListMarketDataSets(ctx)
		if err != nil {
			return fmt.Errorf("query market data sets: %w", err)
		}
		state.MarketDataSets = datasets
		return nil
	})
}

func (p *GRPCLiveStateProvider) schedulePartyQuery(ctx context.Context, g *errgroup.Group, state *LiveState) {
	g.Go(func() error {
		orgs, err := p.party.ListOrganizations(ctx)
		if err != nil {
			return fmt.Errorf("query organizations: %w", err)
		}
		state.Organizations = orgs
		return nil
	})
}

func (p *GRPCLiveStateProvider) scheduleInternalAccountQuery(ctx context.Context, g *errgroup.Group, state *LiveState) {
	g.Go(func() error {
		accounts, err := p.internalAccount.ListInternalAccounts(ctx)
		if err != nil {
			return fmt.Errorf("query internal accounts: %w", err)
		}
		state.InternalAccounts = accounts
		return nil
	})
}

func (p *GRPCLiveStateProvider) scheduleOperationalGatewayQueries(ctx context.Context, g *errgroup.Group, state *LiveState) {
	g.Go(func() error {
		connections, err := p.operationalGateway.ListProviderConnections(ctx)
		if err != nil {
			return fmt.Errorf("query provider connections: %w", err)
		}
		state.ProviderConnections = connections
		return nil
	})
	g.Go(func() error {
		routes, err := p.operationalGateway.ListInstructionRoutes(ctx)
		if err != nil {
			return fmt.Errorf("query instruction routes: %w", err)
		}
		state.InstructionRoutes = routes
		return nil
	})
}
