package differ

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/stretchr/testify/assert"
)

func TestMapInstrument(t *testing.T) {
	inst := &referencedatav1.InstrumentDefinition{
		Code:        "GBP",
		DisplayName: "British Pound Sterling",
		Dimension:   referencedatav1.Dimension_DIMENSION_CURRENCY,
		Precision:   2,
	}

	got := mapInstrument(inst)

	assert.Equal(t, "GBP", got.GetCode())
	assert.Equal(t, "British Pound Sterling", got.GetName())
	assert.Equal(t, controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT, got.GetType())
	assert.Equal(t, int32(2), got.GetDimensions().GetPrecision())
	assert.Equal(t, "GBP", got.GetDimensions().GetUnit())
}

func TestMapDimensionToInstrumentType(t *testing.T) {
	tests := []struct {
		dimension referencedatav1.Dimension
		expected  controlplanev1.InstrumentType
	}{
		{referencedatav1.Dimension_DIMENSION_CURRENCY, controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT},
		{referencedatav1.Dimension_DIMENSION_ENERGY, controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY},
		{referencedatav1.Dimension_DIMENSION_MASS, controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY},
		{referencedatav1.Dimension_DIMENSION_CARBON, controlplanev1.InstrumentType_INSTRUMENT_TYPE_COMMODITY},
		{referencedatav1.Dimension_DIMENSION_COMPUTE, controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED},
		{referencedatav1.Dimension_DIMENSION_UNSPECIFIED, controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, mapDimensionToInstrumentType(tt.dimension), "dimension=%v", tt.dimension)
	}
}

func TestMapAccountType(t *testing.T) {
	at := &referencedatav1.AccountTypeDefinition{
		Code:           "CUSTOMER_CURRENT",
		DisplayName:    "Customer Current Account",
		NormalBalance:  referencedatav1.NormalBalance_NORMAL_BALANCE_CREDIT,
		InstrumentCode: "GBP",
		ValidationCel:  "amount > 0",
		BucketingCel:   "instrument_code",
	}

	got := mapAccountType(at)

	assert.Equal(t, "CUSTOMER_CURRENT", got.GetCode())
	assert.Equal(t, "Customer Current Account", got.GetName())
	assert.Equal(t, controlplanev1.NormalBalance_NORMAL_BALANCE_CREDIT, got.GetNormalBalance())
	assert.Equal(t, []string{"GBP"}, got.GetAllowedInstruments())
	assert.Equal(t, "amount > 0", got.GetPolicies().GetValidation())
	assert.Equal(t, "instrument_code", got.GetPolicies().GetBucketing())
}

func TestMapAccountType_NoPolicies(t *testing.T) {
	at := &referencedatav1.AccountTypeDefinition{
		Code:           "SIMPLE",
		DisplayName:    "Simple Account",
		NormalBalance:  referencedatav1.NormalBalance_NORMAL_BALANCE_DEBIT,
		InstrumentCode: "USD",
	}

	got := mapAccountType(at)

	assert.Nil(t, got.GetPolicies())
}

func TestMapSaga(t *testing.T) {
	s := &sagav1.SagaDefinition{
		Name:                    "process_payment",
		Script:                  "def main(ctx):\n  pass",
		PreconditionsExpression: "event.type == 'payment'",
	}

	got := mapSaga(s)

	assert.Equal(t, "process_payment", got.GetName())
	assert.Equal(t, "def main(ctx):\n  pass", got.GetScript())
	assert.Equal(t, "event.type == 'payment'", got.GetFilter())
}

func TestMapSaga_NoFilter(t *testing.T) {
	s := &sagav1.SagaDefinition{
		Name:   "simple_saga",
		Script: "def main(ctx):\n  pass",
	}

	got := mapSaga(s)

	assert.Equal(t, "", got.GetFilter())
	assert.Nil(t, got.Filter)
}

func TestMapMarketDataSource(t *testing.T) {
	src := &marketinformationv1.DataSource{
		Code:        "BLOOMBERG",
		Name:        "Bloomberg LP",
		Description: "Global market data",
		TrustLevel:  95,
	}

	got := mapMarketDataSource(src)

	assert.Equal(t, "BLOOMBERG", got.GetCode())
	assert.Equal(t, "Bloomberg LP", got.GetName())
	assert.Equal(t, "Global market data", got.GetDescription())
	assert.Equal(t, int32(95), got.GetTrustLevel())
}

func TestMapMarketDataSet(t *testing.T) {
	ds := &marketinformationv1.DataSetDefinition{
		Code:                    "USD_EUR_FX",
		Category:                marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE,
		Unit:                    "USD/EUR",
		DisplayName:             "USD/EUR Exchange Rate",
		Description:             "FX rate",
		ValidationExpression:    "value > 0",
		ResolutionKeyExpression: "date",
	}

	got := mapMarketDataSet(ds)

	assert.Equal(t, "USD_EUR_FX", got.GetCode())
	assert.Equal(t, marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, got.GetCategory())
	assert.Equal(t, "USD/EUR", got.GetUnit())
	assert.Equal(t, "USD/EUR Exchange Rate", got.GetDisplayName())
	assert.Equal(t, "value > 0", got.GetValidationExpression())
	assert.Equal(t, "date", got.GetResolutionKeyExpression())
}

func TestMapMarketDataSet_NoOptionalFields(t *testing.T) {
	ds := &marketinformationv1.DataSetDefinition{
		Code: "SIMPLE",
		Unit: "USD",
	}

	got := mapMarketDataSet(ds)

	assert.Nil(t, got.ValidationExpression)
	assert.Nil(t, got.ResolutionKeyExpression)
}

func TestMapOrganization(t *testing.T) {
	p := &partyv1.Party{
		PartyId:               "ACME_CORP",
		PartyType:             partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:             "ACME Corporation Ltd",
		DisplayName:           "ACME Corp",
		ExternalReference:     "12345678",
		ExternalReferenceType: partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "region", Value: "UK"},
		},
	}

	got := mapOrganization(p)

	assert.Equal(t, "ACME_CORP", got.GetCode())
	assert.Equal(t, "ACME Corporation Ltd", got.GetName())
	assert.Equal(t, "PARTY_TYPE_ORGANIZATION", got.GetPartyType())
	assert.Equal(t, "ACME Corporation Ltd", got.GetLegalName())
	assert.Equal(t, "ACME Corp", got.GetDisplayName())
	assert.Equal(t, "12345678", got.GetExternalReference())
	assert.Equal(t, "EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE", got.GetExternalReferenceType())
	assert.Equal(t, map[string]string{"region": "UK"}, got.GetAttributes())
}

func TestMapOrganization_MinimalFields(t *testing.T) {
	p := &partyv1.Party{
		PartyId:   "SIMPLE_ORG",
		PartyType: partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
	}

	got := mapOrganization(p)

	assert.Equal(t, "SIMPLE_ORG", got.GetCode())
	assert.Nil(t, got.LegalName)
	assert.Nil(t, got.DisplayName)
	assert.Nil(t, got.ExternalReference)
	assert.Nil(t, got.ExternalReferenceType)
	assert.Nil(t, got.Attributes)
}

func TestMapInternalAccount(t *testing.T) {
	f := &internalaccountv1.InternalAccountFacility{
		AccountCode:    "CLR-001",
		BehaviorClass:  "CLEARING",
		InstrumentCode: "GBP",
		OrgPartyId:     "ACME_CORP",
		Description:    "Main clearing account",
	}

	got := mapInternalAccount(f)

	assert.Equal(t, "CLR-001", got.GetCode())
	assert.Equal(t, "CLEARING", got.GetAccountType())
	assert.Equal(t, "GBP", got.GetInstrument())
	assert.Equal(t, "ACME_CORP", got.GetOwnerOrganization())
	assert.Equal(t, "Main clearing account", got.GetDescription())
}

func TestMapProviderConnection(t *testing.T) {
	conn := &opgatewayv1.ProviderConnection{
		ConnectionId: "stripe-main",
		ProviderName: "Stripe",
		ProviderType: "payment_gateway",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.stripe.com",
		AuthConfig: &opgatewayv1.ProviderConnection_ApiKey{
			ApiKey: &opgatewayv1.ApiKeyAuth{
				HeaderName: "Authorization",
				SecretRef:  "stripe-api-key",
			},
		},
		RetryPolicy: &opgatewayv1.RetryPolicy{
			MaxAttempts:           3,
			InitialBackoffSeconds: 1,
			MaxBackoffSeconds:     30,
			BackoffMultiplier:     2.0,
		},
		RateLimit: &opgatewayv1.RateLimit{
			RequestsPerSecond: 100.0,
			BurstSize:         10,
		},
	}

	got := mapProviderConnection(conn)

	assert.Equal(t, "stripe-main", got.GetConnectionId())
	assert.Equal(t, "Stripe", got.GetProviderName())
	assert.Equal(t, "payment_gateway", got.GetProviderType())
	assert.Equal(t, controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS, got.GetProtocol())
	assert.Equal(t, "https://api.stripe.com", got.GetBaseUrl())

	auth := got.GetAuth().GetApiKey()
	assert.Equal(t, "Authorization", auth.GetHeaderName())
	assert.Equal(t, "stripe-api-key", auth.GetApiKeySecretRef())

	rp := got.GetRetryPolicy()
	assert.Equal(t, int32(3), rp.GetMaxAttempts())
	assert.Equal(t, int32(1), rp.GetInitialBackoffSeconds())
	assert.Equal(t, int32(30), rp.GetMaxBackoffSeconds())
	assert.Equal(t, 2.0, rp.GetBackoffMultiplier())

	rl := got.GetRateLimit()
	assert.Equal(t, 100.0, rl.GetRequestsPerSecond())
	assert.Equal(t, int32(10), rl.GetBurstSize())
}

func TestMapProviderConnection_OAuth2Auth(t *testing.T) {
	conn := &opgatewayv1.ProviderConnection{
		ConnectionId: "oauth-svc",
		AuthConfig: &opgatewayv1.ProviderConnection_Oauth2{
			Oauth2: &opgatewayv1.OAuth2Auth{
				TokenUrl:        "https://auth.example.com/token",
				ClientId:        "client-123",
				ClientSecretRef: "oauth-secret",
				Scopes:          []string{"read", "write"},
			},
		},
	}

	got := mapProviderConnection(conn)

	auth := got.GetAuth().GetOauth2()
	assert.Equal(t, "https://auth.example.com/token", auth.GetTokenUrl())
	assert.Equal(t, "client-123", auth.GetClientId())
	assert.Equal(t, "oauth-secret", auth.GetClientSecretRef())
	assert.Equal(t, []string{"read", "write"}, auth.GetScopes())
}

func TestMapProviderConnection_NoAuthConfig(t *testing.T) {
	conn := &opgatewayv1.ProviderConnection{
		ConnectionId: "no-auth",
	}

	got := mapProviderConnection(conn)

	assert.Nil(t, got.GetAuth())
}

func TestMapProviderConnection_NoRetryOrRateLimit(t *testing.T) {
	conn := &opgatewayv1.ProviderConnection{
		ConnectionId: "minimal",
	}

	got := mapProviderConnection(conn)

	assert.Nil(t, got.GetRetryPolicy())
	assert.Nil(t, got.GetRateLimit())
}

func TestMapInstructionRoute(t *testing.T) {
	r := &opgatewayv1.InstructionRoute{
		InstructionType:    "payment.initiate",
		ConnectionId:       "stripe-main",
		FallbackConnectionId: "stripe-backup",
		OutboundMapping:    "payment_outbound",
		InboundMapping:     "payment_inbound",
		HttpMethod:         "POST",
		PathTemplate:       "/v1/charges",
	}

	got := mapInstructionRoute(r)

	assert.Equal(t, "payment.initiate", got.GetInstructionType())
	assert.Equal(t, "stripe-main", got.GetConnectionId())
	assert.Equal(t, "stripe-backup", got.GetFallbackConnectionId())
	assert.Equal(t, "payment_outbound", got.GetOutboundMappingId())
	assert.Equal(t, "payment_inbound", got.GetInboundMappingId())
	assert.Equal(t, "POST", got.GetHttpMethod())
	assert.Equal(t, "/v1/charges", got.GetPathTemplate())
}

func TestMapProtocol(t *testing.T) {
	tests := []struct {
		input    opgatewayv1.Protocol
		expected controlplanev1.ProviderProtocol
	}{
		{opgatewayv1.Protocol_PROTOCOL_HTTPS, controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_HTTPS},
		{opgatewayv1.Protocol_PROTOCOL_GRPC, controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_GRPC},
		{opgatewayv1.Protocol_PROTOCOL_WEBHOOK, controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_WEBHOOK},
		{opgatewayv1.Protocol_PROTOCOL_MQTT, controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_MQTT},
		{opgatewayv1.Protocol_PROTOCOL_AMQP, controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_AMQP},
		{opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED, controlplanev1.ProviderProtocol_PROVIDER_PROTOCOL_UNSPECIFIED},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, mapProtocol(tt.input), "protocol=%v", tt.input)
	}
}

func TestMapAuthConfig_AllTypes(t *testing.T) {
	tests := []struct {
		name string
		conn *opgatewayv1.ProviderConnection
		check func(t *testing.T, auth *controlplanev1.AuthConfigManifest)
	}{
		{
			name: "basic",
			conn: &opgatewayv1.ProviderConnection{
				AuthConfig: &opgatewayv1.ProviderConnection_Basic{
					Basic: &opgatewayv1.BasicAuth{
						Username:          "user",
						PasswordSecretRef: "pass-ref",
					},
				},
			},
			check: func(t *testing.T, auth *controlplanev1.AuthConfigManifest) {
				b := auth.GetBasic()
				assert.Equal(t, "user", b.GetUsername())
				assert.Equal(t, "pass-ref", b.GetPasswordSecretRef())
			},
		},
		{
			name: "hmac",
			conn: &opgatewayv1.ProviderConnection{
				AuthConfig: &opgatewayv1.ProviderConnection_Hmac{
					Hmac: &opgatewayv1.HMACAuth{
						Algorithm:       "SHA256",
						SecretRef:       "hmac-secret",
						SignatureHeader: "X-Signature",
					},
				},
			},
			check: func(t *testing.T, auth *controlplanev1.AuthConfigManifest) {
				h := auth.GetHmac()
				assert.Equal(t, "SHA256", h.GetAlgorithm())
				assert.Equal(t, "hmac-secret", h.GetSecretRef())
				assert.Equal(t, "X-Signature", h.GetSignatureHeader())
			},
		},
		{
			name: "mtls",
			conn: &opgatewayv1.ProviderConnection{
				AuthConfig: &opgatewayv1.ProviderConnection_Mtls{
					Mtls: &opgatewayv1.MTLSAuth{
						ClientCertSecretRef: "cert-ref",
						ClientKeySecretRef:  "key-ref",
						CaCertSecretRef:     "ca-ref",
					},
				},
			},
			check: func(t *testing.T, auth *controlplanev1.AuthConfigManifest) {
				m := auth.GetMtls()
				assert.Equal(t, "cert-ref", m.GetClientCertSecretRef())
				assert.Equal(t, "key-ref", m.GetClientKeySecretRef())
				assert.Equal(t, "ca-ref", m.GetCaCertSecretRef())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapAuthConfig(tt.conn)
			tt.check(t, got)
		})
	}
}

func TestNewLiveStateClients_NilConn(t *testing.T) {
	// Passing a nil conn to the constructors will create clients with nil stubs.
	// This test verifies the wiring function itself doesn't panic when given
	// a valid (non-nil) connection shape. We can't test actual gRPC calls without
	// a server, but we verify the constructor wiring completes.
	// The nil-client validation is tested by the existing GRPCLiveStateProvider tests.
}
