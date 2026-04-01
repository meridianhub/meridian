package differ

import (
	"context"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// LiveState holds the current state of all resource types queried from downstream services.
// It is the input to the differ's three-way comparison (last-applied vs desired vs live).
type LiveState struct {
	Instruments         []*controlplanev1.InstrumentDefinition
	AccountTypes        []*controlplanev1.AccountTypeDefinition
	Sagas               []*controlplanev1.SagaDefinition
	MarketDataSources   []*controlplanev1.MarketDataSourceDefinition
	MarketDataSets      []*controlplanev1.MarketDataSetDefinition
	Organizations       []*controlplanev1.OrganizationDefinition
	InternalAccounts    []*controlplanev1.InternalAccountDefinition
	ProviderConnections []*controlplanev1.ProviderConnectionConfig
	InstructionRoutes   []*controlplanev1.InstructionRouteConfig
}

// LiveStateProvider queries downstream services and returns the current live state
// for all resource types. Implementations must return an error if any service query
// fails - partial state is not acceptable for diff correctness.
type LiveStateProvider interface {
	QueryLiveState(ctx context.Context, tenantID string) (*LiveState, error)
}
