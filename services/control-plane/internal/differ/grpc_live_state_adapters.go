package differ

import (
	"context"
	"fmt"
	"strings"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// isEmptyState returns true if the error indicates the service has no data for
// this tenant rather than a real failure. This covers two cases:
//   - codes.Unimplemented: the gRPC service is not registered on the target
//     server (e.g., operational-gateway runs as a separate process)
//   - "schema not provisioned" / "schema does not exist": the tenant's schema
//     has not been created in this service's database yet (first manifest apply)
//
// Returning true allows callers to treat the resource type as having no live
// state rather than failing the entire diff.
func isEmptyState(err error) bool {
	if status.Code(err) == codes.Unimplemented {
		return true
	}
	msg := status.Convert(err).Message()
	return strings.Contains(msg, "schema not provisioned") ||
		strings.Contains(msg, "schema does not exist")
}

// defaultPageSize is the page size used when paginating through list RPCs.
const defaultPageSize = 500

// ── Reference Data Adapter ──────────────────────────────────────────────────

// GRPCReferenceDataClient implements ReferenceDataClient by calling the
// reference-data and account-type gRPC services and mapping responses to
// control-plane manifest types.
type GRPCReferenceDataClient struct {
	instruments  referencedatav1.ReferenceDataServiceClient
	accountTypes referencedatav1.AccountTypeRegistryServiceClient
}

// NewGRPCReferenceDataClient creates a new adapter from a gRPC connection.
func NewGRPCReferenceDataClient(conn *grpc.ClientConn) *GRPCReferenceDataClient {
	return &GRPCReferenceDataClient{
		instruments:  referencedatav1.NewReferenceDataServiceClient(conn),
		accountTypes: referencedatav1.NewAccountTypeRegistryServiceClient(conn),
	}
}

// ListInstruments implements ReferenceDataClient.
func (c *GRPCReferenceDataClient) ListInstruments(ctx context.Context) ([]*controlplanev1.InstrumentDefinition, error) {
	var result []*controlplanev1.InstrumentDefinition
	pageToken := ""
	for {
		resp, err := c.instruments.ListInstruments(ctx, &referencedatav1.ListInstrumentsRequest{
			PageSize:  defaultPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list instruments: %w", err)
		}
		for _, inst := range resp.GetInstruments() {
			result = append(result, mapInstrument(inst))
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return result, nil
		}
	}
}

// ListAccountTypes implements ReferenceDataClient.
func (c *GRPCReferenceDataClient) ListAccountTypes(ctx context.Context) ([]*controlplanev1.AccountTypeDefinition, error) {
	var result []*controlplanev1.AccountTypeDefinition
	pageToken := ""
	for {
		resp, err := c.accountTypes.ListAll(ctx, &referencedatav1.ListAllRequest{
			PageSize:  defaultPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list account types: %w", err)
		}
		for _, at := range resp.GetDefinitions() {
			result = append(result, mapAccountType(at))
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return result, nil
		}
	}
}

var _ ReferenceDataClient = (*GRPCReferenceDataClient)(nil)

// ── Saga Registry Adapter ───────────────────────────────────────────────────

// GRPCSagaRegistryClient implements SagaRegistryClient by calling the saga
// registry gRPC service and mapping responses to control-plane manifest types.
type GRPCSagaRegistryClient struct {
	client sagav1.SagaRegistryServiceClient
}

// NewGRPCSagaRegistryClient creates a new adapter from a gRPC connection.
func NewGRPCSagaRegistryClient(conn *grpc.ClientConn) *GRPCSagaRegistryClient {
	return &GRPCSagaRegistryClient{
		client: sagav1.NewSagaRegistryServiceClient(conn),
	}
}

// ListSagas implements SagaRegistryClient.
func (c *GRPCSagaRegistryClient) ListSagas(ctx context.Context) ([]*controlplanev1.SagaDefinition, error) {
	var result []*controlplanev1.SagaDefinition
	pageToken := ""
	for {
		resp, err := c.client.ListSagas(ctx, &sagav1.ListSagasRequest{
			PageSize:  defaultPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list sagas: %w", err)
		}
		for _, s := range resp.GetSagas() {
			result = append(result, mapSaga(s))
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return result, nil
		}
	}
}

var _ SagaRegistryClient = (*GRPCSagaRegistryClient)(nil)

// ── Market Information Adapter ──────────────────────────────────────────────

// GRPCMarketInformationClient implements MarketInformationClient by calling the
// market-information gRPC service and mapping responses to control-plane types.
type GRPCMarketInformationClient struct {
	client marketinformationv1.MarketInformationServiceClient
}

// NewGRPCMarketInformationClient creates a new adapter from a gRPC connection.
func NewGRPCMarketInformationClient(conn *grpc.ClientConn) *GRPCMarketInformationClient {
	return &GRPCMarketInformationClient{
		client: marketinformationv1.NewMarketInformationServiceClient(conn),
	}
}

// ListMarketDataSources implements MarketInformationClient.
func (c *GRPCMarketInformationClient) ListMarketDataSources(ctx context.Context) ([]*controlplanev1.MarketDataSourceDefinition, error) {
	var result []*controlplanev1.MarketDataSourceDefinition
	pageToken := ""
	for {
		resp, err := c.client.ListDataSources(ctx, &marketinformationv1.ListDataSourcesRequest{
			PageSize:  defaultPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list data sources: %w", err)
		}
		for _, src := range resp.GetSources() {
			result = append(result, mapMarketDataSource(src))
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return result, nil
		}
	}
}

// ListMarketDataSets implements MarketInformationClient.
func (c *GRPCMarketInformationClient) ListMarketDataSets(ctx context.Context) ([]*controlplanev1.MarketDataSetDefinition, error) {
	var result []*controlplanev1.MarketDataSetDefinition
	pageToken := ""
	for {
		resp, err := c.client.ListDataSets(ctx, &marketinformationv1.ListDataSetsRequest{
			PageSize:  defaultPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list data sets: %w", err)
		}
		for _, ds := range resp.GetDatasets() {
			result = append(result, mapMarketDataSet(ds))
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return result, nil
		}
	}
}

var _ MarketInformationClient = (*GRPCMarketInformationClient)(nil)

// ── Party Adapter ───────────────────────────────────────────────────────────

// GRPCPartyClient implements PartyClient by calling the party gRPC service
// and mapping organization-type parties to control-plane OrganizationDefinition.
type GRPCPartyClient struct {
	client partyv1.PartyServiceClient
}

// NewGRPCPartyClient creates a new adapter from a gRPC connection.
func NewGRPCPartyClient(conn *grpc.ClientConn) *GRPCPartyClient {
	return &GRPCPartyClient{
		client: partyv1.NewPartyServiceClient(conn),
	}
}

// ListOrganizations implements PartyClient.
func (c *GRPCPartyClient) ListOrganizations(ctx context.Context) ([]*controlplanev1.OrganizationDefinition, error) {
	var result []*controlplanev1.OrganizationDefinition
	pageToken := ""
	for {
		resp, err := c.client.ListParties(ctx, &partyv1.ListPartiesRequest{
			PageSize:  defaultPageSize,
			PageToken: pageToken,
			PartyType: partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list organizations: %w", err)
		}
		for _, p := range resp.GetParties() {
			if p.GetPartyType() != partyv1.PartyType_PARTY_TYPE_ORGANIZATION {
				continue
			}
			result = append(result, mapOrganization(p))
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return result, nil
		}
	}
}

var _ PartyClient = (*GRPCPartyClient)(nil)

// ── Internal Account Adapter ────────────────────────────────────────────────

// GRPCInternalAccountClient implements InternalAccountClient by calling the
// internal-account gRPC service and mapping responses to control-plane types.
type GRPCInternalAccountClient struct {
	client internalaccountv1.InternalAccountServiceClient
}

// NewGRPCInternalAccountClient creates a new adapter from a gRPC connection.
func NewGRPCInternalAccountClient(conn *grpc.ClientConn) *GRPCInternalAccountClient {
	return &GRPCInternalAccountClient{
		client: internalaccountv1.NewInternalAccountServiceClient(conn),
	}
}

// ListInternalAccounts implements InternalAccountClient.
func (c *GRPCInternalAccountClient) ListInternalAccounts(ctx context.Context) ([]*controlplanev1.InternalAccountDefinition, error) {
	var result []*controlplanev1.InternalAccountDefinition
	pageToken := ""
	for {
		resp, err := c.client.ListInternalAccounts(ctx, &internalaccountv1.ListInternalAccountsRequest{
			Pagination: &commonv1.Pagination{
				PageSize:  defaultPageSize,
				PageToken: pageToken,
			},
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list internal accounts: %w", err)
		}
		for _, f := range resp.GetFacilities() {
			result = append(result, mapInternalAccount(f))
		}
		pag := resp.GetPagination()
		if pag == nil || pag.GetNextPageToken() == "" {
			return result, nil
		}
		pageToken = pag.GetNextPageToken()
	}
}

var _ InternalAccountClient = (*GRPCInternalAccountClient)(nil)

// ── Operational Gateway Adapter ─────────────────────────────────────────────

// GRPCOperationalGatewayClient implements OperationalGatewayClient by calling
// the operational-gateway gRPC services and mapping responses to control-plane types.
type GRPCOperationalGatewayClient struct {
	connClient  opgatewayv1.ProviderConnectionServiceClient
	routeClient opgatewayv1.InstructionRouteServiceClient
}

// NewGRPCOperationalGatewayClient creates a new adapter from a gRPC connection.
func NewGRPCOperationalGatewayClient(conn *grpc.ClientConn) *GRPCOperationalGatewayClient {
	return &GRPCOperationalGatewayClient{
		connClient:  opgatewayv1.NewProviderConnectionServiceClient(conn),
		routeClient: opgatewayv1.NewInstructionRouteServiceClient(conn),
	}
}

// ListProviderConnections implements OperationalGatewayClient.
func (c *GRPCOperationalGatewayClient) ListProviderConnections(ctx context.Context) ([]*controlplanev1.ProviderConnectionConfig, error) {
	var result []*controlplanev1.ProviderConnectionConfig
	pageToken := ""
	for {
		resp, err := c.connClient.ListConnections(ctx, &opgatewayv1.ListConnectionsRequest{
			Pagination: &commonv1.Pagination{
				PageSize:  defaultPageSize,
				PageToken: pageToken,
			},
		})
		if err != nil {
			if isEmptyState(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list provider connections: %w", err)
		}
		for _, conn := range resp.GetConnections() {
			result = append(result, mapProviderConnection(conn))
		}
		pag := resp.GetPagination()
		if pag == nil || pag.GetNextPageToken() == "" {
			return result, nil
		}
		pageToken = pag.GetNextPageToken()
	}
}

// ListInstructionRoutes implements OperationalGatewayClient.
func (c *GRPCOperationalGatewayClient) ListInstructionRoutes(ctx context.Context) ([]*controlplanev1.InstructionRouteConfig, error) {
	resp, err := c.routeClient.ListRoutes(ctx, &opgatewayv1.ListRoutesRequest{})
	if err != nil {
		if isEmptyState(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list instruction routes: %w", err)
	}
	result := make([]*controlplanev1.InstructionRouteConfig, 0, len(resp.GetRoutes()))
	for _, r := range resp.GetRoutes() {
		result = append(result, mapInstructionRoute(r))
	}
	return result, nil
}

var _ OperationalGatewayClient = (*GRPCOperationalGatewayClient)(nil)

// ── Mapping Functions ───────────────────────────────────────────────────────

func mapInstrument(inst *referencedatav1.InstrumentDefinition) *controlplanev1.InstrumentDefinition {
	return &controlplanev1.InstrumentDefinition{
		Code: inst.GetCode(),
		Name: inst.GetDisplayName(),
		Type: mapDimensionToInstrumentType(inst.GetDimension()),
		Dimensions: &controlplanev1.InstrumentDimensions{
			Unit:      inst.GetCode(),
			Precision: inst.GetPrecision(),
		},
	}
}

func mapDimensionToInstrumentType(d referencedatav1.Dimension) controlplanev1.InstrumentType {
	switch d {
	case referencedatav1.Dimension_DIMENSION_CURRENCY:
		return controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT
	case referencedatav1.Dimension_DIMENSION_ENERGY,
		referencedatav1.Dimension_DIMENSION_MASS,
		referencedatav1.Dimension_DIMENSION_VOLUME,
		referencedatav1.Dimension_DIMENSION_CARBON:
		return controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY
	case referencedatav1.Dimension_DIMENSION_UNSPECIFIED,
		referencedatav1.Dimension_DIMENSION_TIME,
		referencedatav1.Dimension_DIMENSION_COMPUTE,
		referencedatav1.Dimension_DIMENSION_DATA,
		referencedatav1.Dimension_DIMENSION_COUNT:
		return controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED
	}
	return controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED
}

func mapAccountType(at *referencedatav1.AccountTypeDefinition) *controlplanev1.AccountTypeDefinition {
	def := &controlplanev1.AccountTypeDefinition{
		Code:          at.GetCode(),
		Name:          at.GetDisplayName(),
		NormalBalance: controlplanev1.NormalBalance(at.GetNormalBalance()),
	}
	if ic := at.GetInstrumentCode(); ic != "" {
		def.AllowedInstruments = []string{ic}
	}
	if at.GetValidationCel() != "" || at.GetBucketingCel() != "" {
		def.Policies = &controlplanev1.AccountTypePolicies{
			Validation: at.GetValidationCel(),
			Bucketing:  at.GetBucketingCel(),
		}
	}
	return def
}

func mapSaga(s *sagav1.SagaDefinition) *controlplanev1.SagaDefinition {
	def := &controlplanev1.SagaDefinition{
		Name:   s.GetName(),
		Script: s.GetScript(),
	}
	if v := s.GetPreconditionsExpression(); v != "" {
		def.Filter = &v
	}
	return def
}

func mapMarketDataSource(src *marketinformationv1.DataSource) *controlplanev1.MarketDataSourceDefinition {
	return &controlplanev1.MarketDataSourceDefinition{
		Code:        src.GetCode(),
		Name:        src.GetName(),
		Description: src.GetDescription(),
		TrustLevel:  src.GetTrustLevel(),
	}
}

func mapMarketDataSet(ds *marketinformationv1.DataSetDefinition) *controlplanev1.MarketDataSetDefinition {
	def := &controlplanev1.MarketDataSetDefinition{
		Code:        ds.GetCode(),
		Category:    ds.GetCategory(),
		Unit:        ds.GetUnit(),
		DisplayName: ds.GetDisplayName(),
		Description: ds.GetDescription(),
	}
	if v := ds.GetValidationExpression(); v != "" {
		def.ValidationExpression = &v
	}
	if v := ds.GetResolutionKeyExpression(); v != "" {
		def.ResolutionKeyExpression = &v
	}
	return def
}

func mapOrganization(p *partyv1.Party) *controlplanev1.OrganizationDefinition {
	// Derive the manifest code from attributes or external_reference.
	// The Party service has no dedicated "code" field - party_id is a UUID.
	// The manifest applier stores the org code as external_reference (fallback)
	// or the _manifest_code attribute.
	code := ""
	for _, a := range p.GetAttributes() {
		if a.GetKey() == "_manifest_code" {
			code = a.GetValue()
			break
		}
	}
	if code == "" {
		code = p.GetExternalReference()
	}
	if code == "" {
		code = p.GetPartyId()
	}

	// Strip proto enum prefix: "PARTY_TYPE_ORGANIZATION" -> "ORGANIZATION"
	partyType := strings.TrimPrefix(p.GetPartyType().String(), "PARTY_TYPE_")

	def := &controlplanev1.OrganizationDefinition{
		Code:      code,
		Name:      p.GetLegalName(),
		PartyType: partyType,
	}
	if v := p.GetLegalName(); v != "" {
		def.LegalName = &v
	}
	if v := p.GetDisplayName(); v != "" {
		def.DisplayName = &v
	}
	if v := p.GetExternalReference(); v != "" {
		def.ExternalReference = &v
	}
	if p.GetExternalReferenceType() != partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED {
		s := p.GetExternalReferenceType().String()
		def.ExternalReferenceType = &s
	}
	if attrs := p.GetAttributes(); len(attrs) > 0 {
		def.Attributes = make(map[string]string, len(attrs))
		for _, a := range attrs {
			def.Attributes[a.GetKey()] = a.GetValue()
		}
	}
	return def
}

func mapInternalAccount(f *internalaccountv1.InternalAccountFacility) *controlplanev1.InternalAccountDefinition {
	return &controlplanev1.InternalAccountDefinition{
		Code:              f.GetAccountCode(),
		AccountType:       f.GetBehaviorClass(),
		Instrument:        f.GetInstrumentCode(),
		OwnerOrganization: f.GetOrgPartyId(),
		Description:       f.GetDescription(),
	}
}

func mapProviderConnection(conn *opgatewayv1.ProviderConnection) *controlplanev1.ProviderConnectionConfig {
	cfg := &controlplanev1.ProviderConnectionConfig{
		ConnectionId: conn.GetConnectionId(),
		ProviderName: conn.GetProviderName(),
		ProviderType: conn.GetProviderType(),
		Protocol:     mapProtocol(conn.GetProtocol()),
		BaseUrl:      conn.GetBaseUrl(),
		Auth:         mapAuthConfig(conn),
	}
	if rp := conn.GetRetryPolicy(); rp != nil {
		cfg.RetryPolicy = &controlplanev1.RetryPolicyConfig{
			MaxAttempts:           rp.GetMaxAttempts(),
			InitialBackoffSeconds: rp.GetInitialBackoffSeconds(),
			MaxBackoffSeconds:     rp.GetMaxBackoffSeconds(),
			BackoffMultiplier:     rp.GetBackoffMultiplier(),
		}
	}
	if rl := conn.GetRateLimit(); rl != nil {
		cfg.RateLimit = &controlplanev1.RateLimitConfig{
			RequestsPerSecond: rl.GetRequestsPerSecond(),
			BurstSize:         rl.GetBurstSize(),
		}
	}
	return cfg
}

func mapProtocol(p opgatewayv1.Protocol) controlplanev1.ProviderProtocol {
	switch p {
	case opgatewayv1.Protocol_PROTOCOL_HTTPS:
		return controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS
	case opgatewayv1.Protocol_PROTOCOL_GRPC:
		return controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_GRPC
	case opgatewayv1.Protocol_PROTOCOL_WEBHOOK:
		return controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_WEBHOOK
	case opgatewayv1.Protocol_PROTOCOL_MQTT:
		return controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_MQTT
	case opgatewayv1.Protocol_PROTOCOL_AMQP:
		return controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_AMQP
	case opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED:
		return controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED
	}
	return controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED
}

func mapAuthConfig(conn *opgatewayv1.ProviderConnection) *controlplanev1.AuthConfigManifest {
	switch auth := conn.GetAuthConfig().(type) {
	case *opgatewayv1.ProviderConnection_ApiKey:
		ak := auth.ApiKey
		return &controlplanev1.AuthConfigManifest{
			AuthConfig: &controlplanev1.AuthConfigManifest_ApiKey{
				ApiKey: &controlplanev1.ApiKeyAuthConfig{
					HeaderName:      ak.GetHeaderName(),
					ApiKeySecretRef: ak.GetSecretRef(),
				},
			},
		}
	case *opgatewayv1.ProviderConnection_Basic:
		b := auth.Basic
		return &controlplanev1.AuthConfigManifest{
			AuthConfig: &controlplanev1.AuthConfigManifest_Basic{
				Basic: &controlplanev1.BasicAuthConfig{
					Username:          b.GetUsername(),
					PasswordSecretRef: b.GetPasswordSecretRef(),
				},
			},
		}
	case *opgatewayv1.ProviderConnection_Oauth2:
		o := auth.Oauth2
		return &controlplanev1.AuthConfigManifest{
			AuthConfig: &controlplanev1.AuthConfigManifest_Oauth2{
				Oauth2: &controlplanev1.OAuth2AuthConfig{
					TokenUrl:        o.GetTokenUrl(),
					ClientId:        o.GetClientId(),
					ClientSecretRef: o.GetClientSecretRef(),
					Scopes:          o.GetScopes(),
				},
			},
		}
	case *opgatewayv1.ProviderConnection_Hmac:
		h := auth.Hmac
		return &controlplanev1.AuthConfigManifest{
			AuthConfig: &controlplanev1.AuthConfigManifest_Hmac{
				Hmac: &controlplanev1.HMACAuthConfig{
					Algorithm:       h.GetAlgorithm(),
					SecretRef:       h.GetSecretRef(),
					SignatureHeader: h.GetSignatureHeader(),
				},
			},
		}
	case *opgatewayv1.ProviderConnection_Mtls:
		m := auth.Mtls
		return &controlplanev1.AuthConfigManifest{
			AuthConfig: &controlplanev1.AuthConfigManifest_Mtls{
				Mtls: &controlplanev1.MTLSAuthConfig{
					ClientCertSecretRef: m.GetClientCertSecretRef(),
					ClientKeySecretRef:  m.GetClientKeySecretRef(),
					CaCertSecretRef:     m.GetCaCertSecretRef(),
				},
			},
		}
	default:
		return nil
	}
}

func mapInstructionRoute(r *opgatewayv1.InstructionRoute) *controlplanev1.InstructionRouteConfig {
	return &controlplanev1.InstructionRouteConfig{
		InstructionType:      r.GetInstructionType(),
		ConnectionId:         r.GetConnectionId(),
		FallbackConnectionId: r.GetFallbackConnectionId(),
		OutboundMappingId:    r.GetOutboundMapping(),
		InboundMappingId:     r.GetInboundMapping(),
		HttpMethod:           r.GetHttpMethod(),
		PathTemplate:         r.GetPathTemplate(),
	}
}

// NewLiveStateClients creates all six differ client adapters from a single gRPC
// connection and returns a fully-wired GRPCLiveStateProvider.
func NewLiveStateClients(conn *grpc.ClientConn) (*GRPCLiveStateProvider, error) {
	return NewGRPCLiveStateProvider(
		NewGRPCReferenceDataClient(conn),
		NewGRPCSagaRegistryClient(conn),
		NewGRPCMarketInformationClient(conn),
		NewGRPCPartyClient(conn),
		NewGRPCInternalAccountClient(conn),
		NewGRPCOperationalGatewayClient(conn),
	)
}
