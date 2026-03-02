package applier

import (
	"errors"
	"fmt"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/grpc"
)

// ErrUnsupportedAuthType is returned when an unrecognized auth_type is provided.
var ErrUnsupportedAuthType = errors.New("unsupported auth_type")

// OperationalGatewayClient wraps the operational-gateway gRPC clients to implement
// OperationalGatewayService for use as a saga handler dependency.
//
// The client translates between the flat map[string]any parameter convention used
// by saga handlers and the typed proto messages required by the gRPC service.
type OperationalGatewayClient struct {
	connClient  opgatewayv1.ProviderConnectionServiceClient
	routeClient opgatewayv1.InstructionRouteServiceClient
}

// NewOperationalGatewayClient creates a new OperationalGatewayClient from a gRPC connection.
func NewOperationalGatewayClient(conn *grpc.ClientConn) *OperationalGatewayClient {
	return &OperationalGatewayClient{
		connClient:  opgatewayv1.NewProviderConnectionServiceClient(conn),
		routeClient: opgatewayv1.NewInstructionRouteServiceClient(conn),
	}
}

// UpsertConnection implements OperationalGatewayService.
// Converts Starlark params to a UpsertConnectionRequest and calls the gRPC service.
func (c *OperationalGatewayClient) UpsertConnection(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req, err := buildUpsertConnectionRequest(params)
	if err != nil {
		return nil, fmt.Errorf("build upsert connection request: %w", err)
	}

	resp, err := c.connClient.UpsertConnection(ctx.Context, req)
	if err != nil {
		return nil, fmt.Errorf("upsert connection: %w", err)
	}

	conn := resp.GetConnection()
	return map[string]any{
		"connection_id": conn.GetConnectionId(),
		"status":        "UPSERTED",
	}, nil
}

// UpsertRoute implements OperationalGatewayService.
// Converts Starlark params to a UpsertRouteRequest and calls the gRPC service.
func (c *OperationalGatewayClient) UpsertRoute(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
	req, err := buildUpsertRouteRequest(params)
	if err != nil {
		return nil, fmt.Errorf("build upsert route request: %w", err)
	}

	resp, err := c.routeClient.UpsertRoute(ctx.Context, req)
	if err != nil {
		return nil, fmt.Errorf("upsert route: %w", err)
	}

	route := resp.GetRoute()
	return map[string]any{
		"instruction_type": route.GetInstructionType(),
		"status":           "UPSERTED",
	}, nil
}

// buildUpsertConnectionRequest constructs a UpsertConnectionRequest from Starlark params.
// The params map is expected to have the structure serialized by buildSagaInput in executor.go.
func buildUpsertConnectionRequest(params map[string]any) (*opgatewayv1.UpsertConnectionRequest, error) {
	req := &opgatewayv1.UpsertConnectionRequest{}

	req.ConnectionId, _ = params["connection_id"].(string)
	req.ProviderName, _ = params["provider_name"].(string)
	req.ProviderType, _ = params["provider_type"].(string)
	req.BaseUrl, _ = params["base_url"].(string)

	protocolStr, _ := params["protocol"].(string)
	req.Protocol = parseProtocol(protocolStr)

	authType, _ := params["auth_type"].(string)
	authConfig, _ := params["auth_config"].(map[string]any)
	if err := applyAuthConfig(req, authType, authConfig); err != nil {
		return nil, err
	}

	if rp, ok := params["retry_policy"].(map[string]any); ok && len(rp) > 0 {
		req.RetryPolicy = buildRetryPolicy(rp)
	}

	if rl, ok := params["rate_limit_config"].(map[string]any); ok && len(rl) > 0 {
		req.RateLimit = buildRateLimit(rl)
	}

	return req, nil
}

// buildUpsertRouteRequest constructs a UpsertRouteRequest from Starlark params.
func buildUpsertRouteRequest(params map[string]any) (*opgatewayv1.UpsertRouteRequest, error) {
	req := &opgatewayv1.UpsertRouteRequest{}

	req.InstructionType, _ = params["instruction_type"].(string)
	req.ConnectionId, _ = params["connection_id"].(string)
	req.FallbackConnectionId, _ = params["fallback_connection_id"].(string)
	req.OutboundMapping, _ = params["outbound_mapping"].(string)
	req.InboundMapping, _ = params["inbound_mapping"].(string)
	req.HttpMethod, _ = params["http_method"].(string)
	req.PathTemplate, _ = params["path_template"].(string)

	return req, nil
}

// applyAuthConfig populates the auth oneof field in UpsertConnectionRequest.
func applyAuthConfig(req *opgatewayv1.UpsertConnectionRequest, authType string, cfg map[string]any) error {
	switch authType {
	case "api_key":
		req.AuthConfig = &opgatewayv1.UpsertConnectionRequest_ApiKey{
			ApiKey: &opgatewayv1.ApiKeyAuth{
				HeaderName: stringFromMap(cfg, "header_name"),
				SecretRef:  stringFromMap(cfg, "secret_ref"),
			},
		}
	case "basic":
		req.AuthConfig = &opgatewayv1.UpsertConnectionRequest_Basic{
			Basic: &opgatewayv1.BasicAuth{
				Username:          stringFromMap(cfg, "username"),
				PasswordSecretRef: stringFromMap(cfg, "password_ref"),
			},
		}
	case "oauth2":
		var scopes []string
		if raw, ok := cfg["scopes"]; ok {
			switch v := raw.(type) {
			case []string:
				scopes = v
			case []any:
				for _, s := range v {
					if str, ok := s.(string); ok {
						scopes = append(scopes, str)
					}
				}
			}
		}
		req.AuthConfig = &opgatewayv1.UpsertConnectionRequest_Oauth2{
			Oauth2: &opgatewayv1.OAuth2Auth{
				TokenUrl:        stringFromMap(cfg, "token_url"),
				ClientId:        stringFromMap(cfg, "client_id"),
				ClientSecretRef: stringFromMap(cfg, "client_secret_ref"),
				Scopes:          scopes,
			},
		}
	case "hmac":
		req.AuthConfig = &opgatewayv1.UpsertConnectionRequest_Hmac{
			Hmac: &opgatewayv1.HMACAuth{
				Algorithm:       stringFromMap(cfg, "algorithm"),
				SecretRef:       stringFromMap(cfg, "secret_ref"),
				SignatureHeader: stringFromMap(cfg, "signature_header"),
			},
		}
	case "mtls":
		req.AuthConfig = &opgatewayv1.UpsertConnectionRequest_Mtls{
			Mtls: &opgatewayv1.MTLSAuth{
				ClientCertSecretRef: stringFromMap(cfg, "client_cert_ref"),
				ClientKeySecretRef:  stringFromMap(cfg, "client_key_ref"),
				CaCertSecretRef:     stringFromMap(cfg, "ca_cert_ref"),
			},
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAuthType, authType)
	}
	return nil
}

// buildRetryPolicy converts a map to a proto RetryPolicy.
func buildRetryPolicy(m map[string]any) *opgatewayv1.RetryPolicy {
	rp := &opgatewayv1.RetryPolicy{}
	if v, ok := toInt32(m["max_attempts"]); ok {
		rp.MaxAttempts = v
	}
	if v, ok := toInt32(m["initial_backoff_seconds"]); ok {
		rp.InitialBackoffSeconds = v
	}
	if v, ok := toInt32(m["max_backoff_seconds"]); ok {
		rp.MaxBackoffSeconds = v
	}
	if v, ok := toFloat64(m["backoff_multiplier"]); ok {
		rp.BackoffMultiplier = v
	}
	return rp
}

// buildRateLimit converts a map to a proto RateLimit.
func buildRateLimit(m map[string]any) *opgatewayv1.RateLimit {
	rl := &opgatewayv1.RateLimit{}
	if v, ok := toFloat64(m["requests_per_second"]); ok {
		rl.RequestsPerSecond = v
	}
	if v, ok := toInt32(m["burst_size"]); ok {
		rl.BurstSize = v
	}
	return rl
}

// parseProtocol converts a string protocol name to the proto enum value.
// The string comes from the Starlark saga input which uses the enum name
// produced by conn.GetProtocol().String() in buildExecutorInput.
func parseProtocol(s string) opgatewayv1.Protocol {
	switch s {
	case "PROVIDER_PROTOCOL_HTTPS", "PROTOCOL_HTTPS":
		return opgatewayv1.Protocol_PROTOCOL_HTTPS
	case "PROVIDER_PROTOCOL_GRPC", "PROTOCOL_GRPC":
		return opgatewayv1.Protocol_PROTOCOL_GRPC
	case "PROVIDER_PROTOCOL_WEBHOOK", "PROTOCOL_WEBHOOK":
		return opgatewayv1.Protocol_PROTOCOL_WEBHOOK
	case "PROVIDER_PROTOCOL_MQTT", "PROTOCOL_MQTT":
		return opgatewayv1.Protocol_PROTOCOL_MQTT
	case "PROVIDER_PROTOCOL_AMQP", "PROTOCOL_AMQP":
		return opgatewayv1.Protocol_PROTOCOL_AMQP
	default:
		return opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED
	}
}

// stringFromMap safely extracts a string from a map, returning "" if missing or wrong type.
func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// toInt32 converts a numeric value from a map to int32.
func toInt32(v any) (int32, bool) {
	switch n := v.(type) {
	case int:
		return int32(n), true // #nosec G115 — bounded domain value
	case int32:
		return n, true
	case int64:
		return int32(n), true // #nosec G115 — bounded domain value
	case float64:
		return int32(n), true // #nosec G115 — bounded domain value
	case float32:
		return int32(n), true // #nosec G115 — bounded domain value
	}
	return 0, false
}

// toFloat64 converts a numeric value from a map to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// Ensure OperationalGatewayClient implements OperationalGatewayService at compile time.
var _ OperationalGatewayService = (*OperationalGatewayClient)(nil)
