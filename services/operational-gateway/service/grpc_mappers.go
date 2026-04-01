package service

import (
	"time"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ========== Instruction mappers ==========

// instructionToProto converts a domain Instruction to its proto representation.
func instructionToProto(i *domain.Instruction) *opgatewayv1.Instruction {
	p := &opgatewayv1.Instruction{
		Id:                   i.ID.String(),
		TenantId:             i.TenantID.String(),
		InstructionType:      i.InstructionType,
		ProviderConnectionId: i.ProviderConnectionID,
		CorrelationId:        i.CorrelationID,
		CausationId:          i.CausationID,
		Metadata:             i.Metadata,
		Priority:             domainToProtoPriority(i.Priority),
		Status:               domainToProtoStatus(i.Status),
		Payload:              mapToStruct(i.Payload),
		CreatedAt:            timestamppb.New(i.CreatedAt),
		UpdatedAt:            timestamppb.New(i.UpdatedAt),
	}

	if i.ScheduledAt != nil {
		p.ScheduledAt = timestamppb.New(*i.ScheduledAt)
	}
	if i.ExpiresAt != nil {
		p.ExpiresAt = timestamppb.New(*i.ExpiresAt)
	}

	for _, a := range i.Attempts {
		p.Attempts = append(p.Attempts, attemptToProto(a))
	}

	return p
}

// attemptToProto converts a domain InstructionAttempt to proto.
func attemptToProto(a domain.InstructionAttempt) *opgatewayv1.InstructionAttempt {
	return &opgatewayv1.InstructionAttempt{
		AttemptNumber:      int32(a.AttemptNumber), // #nosec G115 — attempt number is a small positive int
		DispatchedAt:       timestamppb.New(a.AttemptedAt),
		ErrorMessage:       a.FailureReason,
		ResponseStatusCode: 0,
		DurationMs:         0,
	}
}

// mapToStruct converts a Go map[string]any to a protobuf Struct.
func mapToStruct(m map[string]any) *structpb.Struct {
	if m == nil {
		return nil
	}
	fields := make(map[string]*structpb.Value, len(m))
	for k, v := range m {
		fields[k] = anyToProtoValue(v)
	}
	return &structpb.Struct{Fields: fields}
}

// anyToProtoValue converts a Go value to a structpb.Value.
func anyToProtoValue(v any) *structpb.Value {
	if v == nil {
		return structpb.NewNullValue()
	}
	switch val := v.(type) {
	case bool:
		return structpb.NewBoolValue(val)
	case float64:
		return structpb.NewNumberValue(val)
	case float32:
		return structpb.NewNumberValue(float64(val))
	case int:
		return structpb.NewNumberValue(float64(val))
	case int32:
		return structpb.NewNumberValue(float64(val))
	case int64:
		return structpb.NewNumberValue(float64(val))
	case uint:
		return structpb.NewNumberValue(float64(val))
	case uint32:
		return structpb.NewNumberValue(float64(val))
	case uint64:
		return structpb.NewNumberValue(float64(val))
	case string:
		return structpb.NewStringValue(val)
	case []any:
		items := make([]*structpb.Value, len(val))
		for i, item := range val {
			items[i] = anyToProtoValue(item)
		}
		return structpb.NewListValue(&structpb.ListValue{Values: items})
	case map[string]any:
		return structpb.NewStructValue(mapToStruct(val))
	default:
		return structpb.NewNullValue()
	}
}

// ========== Status mappers ==========

// domainToProtoStatus converts a domain InstructionStatus to proto InstructionStatus.
func domainToProtoStatus(s domain.InstructionStatus) opgatewayv1.InstructionStatus {
	switch s {
	case domain.InstructionStatusPending:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING
	case domain.InstructionStatusDispatching:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING
	case domain.InstructionStatusDelivered:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED
	case domain.InstructionStatusAcknowledged:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED
	case domain.InstructionStatusRetrying:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING
	case domain.InstructionStatusFailed:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED
	case domain.InstructionStatusExpired:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED
	case domain.InstructionStatusCancelled:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED
	default:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED
	}
}

// protoToDomainStatus converts a proto InstructionStatus to domain InstructionStatus.
func protoToDomainStatus(s opgatewayv1.InstructionStatus) domain.InstructionStatus {
	switch s {
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED:
		return ""
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING:
		return domain.InstructionStatusPending
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING:
		return domain.InstructionStatusDispatching
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED:
		return domain.InstructionStatusDelivered
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED:
		return domain.InstructionStatusAcknowledged
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING:
		return domain.InstructionStatusRetrying
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED:
		return domain.InstructionStatusFailed
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED:
		return domain.InstructionStatusExpired
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED:
		return domain.InstructionStatusCancelled
	}
	return ""
}

// ========== Priority mappers ==========

// domainToProtoPriority converts a domain Priority to proto Priority.
func domainToProtoPriority(p domain.Priority) opgatewayv1.Priority {
	switch p {
	case domain.PriorityLow:
		return opgatewayv1.Priority_PRIORITY_LOW
	case domain.PriorityNormal:
		return opgatewayv1.Priority_PRIORITY_NORMAL
	case domain.PriorityHigh:
		return opgatewayv1.Priority_PRIORITY_HIGH
	case domain.PriorityCritical:
		return opgatewayv1.Priority_PRIORITY_CRITICAL
	default:
		return opgatewayv1.Priority_PRIORITY_NORMAL
	}
}

// protoToDomainPriority converts a proto Priority to a domain Priority.
func protoToDomainPriority(p opgatewayv1.Priority) domain.Priority {
	switch p {
	case opgatewayv1.Priority_PRIORITY_UNSPECIFIED:
		return domain.PriorityNormal
	case opgatewayv1.Priority_PRIORITY_LOW:
		return domain.PriorityLow
	case opgatewayv1.Priority_PRIORITY_NORMAL:
		return domain.PriorityNormal
	case opgatewayv1.Priority_PRIORITY_HIGH:
		return domain.PriorityHigh
	case opgatewayv1.Priority_PRIORITY_CRITICAL:
		return domain.PriorityCritical
	}
	return domain.PriorityNormal
}

// ========== ProviderConnection mappers ==========

// connectionToProto converts a domain ProviderConnection to proto ProviderConnection.
func connectionToProto(c *domain.ProviderConnection) *opgatewayv1.ProviderConnection {
	p := &opgatewayv1.ProviderConnection{
		ConnectionId: c.ConnectionID,
		TenantId:     c.TenantID,
		ProviderName: c.ProviderName,
		ProviderType: c.ProviderType,
		Protocol:     domainToProtoProtocol(c.Protocol),
		BaseUrl:      c.BaseURL,
		HealthStatus: domainToProtoHealthStatus(c.HealthStatus),
		Status:       domainToProtoConnectionStatus(c.Status),
		RetryPolicy: &opgatewayv1.RetryPolicy{
			MaxAttempts:           int32(c.RetryPolicy.MaxAttempts),              // #nosec G115 — bounded domain value
			InitialBackoffSeconds: int32(c.RetryPolicy.InitialBackoff.Seconds()), // #nosec G115
			MaxBackoffSeconds:     int32(c.RetryPolicy.MaxBackoff.Seconds()),     // #nosec G115
			BackoffMultiplier:     c.RetryPolicy.BackoffMultiplier,
		},
		CreatedAt: timestamppb.New(c.CreatedAt),
		UpdatedAt: timestamppb.New(c.UpdatedAt),
	}

	if c.RateLimitConfig.RequestsPerSecond > 0 {
		p.RateLimit = &opgatewayv1.RateLimit{
			RequestsPerSecond: c.RateLimitConfig.RequestsPerSecond,
			BurstSize:         int32(c.RateLimitConfig.BurstSize), // #nosec G115
		}
	}

	if c.LastHealthCheckAt != nil {
		p.LastHealthCheckAt = timestamppb.New(*c.LastHealthCheckAt)
	}
	if c.DeprecatedAt != nil {
		p.DeprecatedAt = timestamppb.New(*c.DeprecatedAt)
	}

	mapAuthConfigToProto(p, c.AuthConfig)

	return p
}

// mapAuthConfigToProto sets the auth config oneof field on the proto ProviderConnection.
func mapAuthConfigToProto(p *opgatewayv1.ProviderConnection, auth domain.AuthConfig) {
	switch a := auth.(type) {
	case *domain.APIKeyAuth:
		p.AuthConfig = &opgatewayv1.ProviderConnection_ApiKey{
			ApiKey: &opgatewayv1.ApiKeyAuth{
				HeaderName: a.HeaderName,
				SecretRef:  a.SecretRef,
			},
		}
	case *domain.BasicAuth:
		p.AuthConfig = &opgatewayv1.ProviderConnection_Basic{
			Basic: &opgatewayv1.BasicAuth{
				Username:          a.Username,
				PasswordSecretRef: a.PasswordRef,
			},
		}
	case *domain.OAuth2Auth:
		p.AuthConfig = &opgatewayv1.ProviderConnection_Oauth2{
			Oauth2: &opgatewayv1.OAuth2Auth{
				TokenUrl:        a.TokenURL,
				ClientId:        a.ClientID,
				ClientSecretRef: a.ClientSecretRef,
				Scopes:          a.Scopes,
			},
		}
	case *domain.HMACAuth:
		p.AuthConfig = &opgatewayv1.ProviderConnection_Hmac{
			Hmac: &opgatewayv1.HMACAuth{
				Algorithm:       a.Algorithm,
				SecretRef:       a.SecretRef,
				SignatureHeader: a.SignatureHeader,
			},
		}
	case *domain.MTLSAuth:
		p.AuthConfig = &opgatewayv1.ProviderConnection_Mtls{
			Mtls: &opgatewayv1.MTLSAuth{
				ClientCertSecretRef: a.ClientCertRef,
				ClientKeySecretRef:  a.ClientKeyRef,
				CaCertSecretRef:     a.CACertRef,
			},
		}
	}
}

// protoToDomainAuthConfig converts a proto auth config oneof to a domain AuthConfig.
func protoToDomainAuthConfig(req *opgatewayv1.UpsertConnectionRequest) domain.AuthConfig {
	switch auth := req.AuthConfig.(type) {
	case *opgatewayv1.UpsertConnectionRequest_ApiKey:
		return &domain.APIKeyAuth{
			HeaderName: auth.ApiKey.HeaderName,
			SecretRef:  auth.ApiKey.SecretRef,
		}
	case *opgatewayv1.UpsertConnectionRequest_Basic:
		return &domain.BasicAuth{
			Username:    auth.Basic.Username,
			PasswordRef: auth.Basic.PasswordSecretRef,
		}
	case *opgatewayv1.UpsertConnectionRequest_Oauth2:
		return &domain.OAuth2Auth{
			TokenURL:        auth.Oauth2.TokenUrl,
			ClientID:        auth.Oauth2.ClientId,
			ClientSecretRef: auth.Oauth2.ClientSecretRef,
			Scopes:          auth.Oauth2.Scopes,
		}
	case *opgatewayv1.UpsertConnectionRequest_Hmac:
		return &domain.HMACAuth{
			Algorithm:       auth.Hmac.Algorithm,
			SecretRef:       auth.Hmac.SecretRef,
			SignatureHeader: auth.Hmac.SignatureHeader,
		}
	case *opgatewayv1.UpsertConnectionRequest_Mtls:
		return &domain.MTLSAuth{
			ClientCertRef: auth.Mtls.ClientCertSecretRef,
			ClientKeyRef:  auth.Mtls.ClientKeySecretRef,
			CACertRef:     auth.Mtls.CaCertSecretRef,
		}
	default:
		return nil
	}
}

// protoToDomainRetryPolicy converts a proto RetryPolicy to domain RetryPolicy.
// Zero-valued fields in the proto message fall back to safe defaults so that callers
// do not need to fully populate the policy in order to get sensible behavior.
func protoToDomainRetryPolicy(r *opgatewayv1.RetryPolicy) domain.RetryPolicy {
	p := domain.RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    1 * time.Second,
		MaxBackoff:        60 * time.Second,
		BackoffMultiplier: 2.0,
	}
	if r == nil {
		return p
	}
	if r.MaxAttempts > 0 {
		p.MaxAttempts = int(r.MaxAttempts)
	}
	if r.InitialBackoffSeconds > 0 {
		p.InitialBackoff = time.Duration(r.InitialBackoffSeconds) * time.Second
	}
	if r.MaxBackoffSeconds > 0 {
		p.MaxBackoff = time.Duration(r.MaxBackoffSeconds) * time.Second
	}
	if r.BackoffMultiplier > 0 {
		p.BackoffMultiplier = r.BackoffMultiplier
	}
	return p
}

// protoToDomainRateLimit converts a proto RateLimit to domain RateLimitConfig.
func protoToDomainRateLimit(r *opgatewayv1.RateLimit) domain.RateLimitConfig {
	if r == nil {
		return domain.RateLimitConfig{}
	}
	return domain.RateLimitConfig{
		RequestsPerSecond: r.RequestsPerSecond,
		BurstSize:         int(r.BurstSize),
	}
}

// ========== Protocol / HealthStatus mappers ==========

// domainToProtoProtocol converts a domain Protocol to proto Protocol.
func domainToProtoProtocol(p domain.Protocol) opgatewayv1.Protocol {
	switch p {
	case domain.ProtocolHTTPS:
		return opgatewayv1.Protocol_PROTOCOL_HTTPS
	case domain.ProtocolGRPC:
		return opgatewayv1.Protocol_PROTOCOL_GRPC
	case domain.ProtocolWebhook:
		return opgatewayv1.Protocol_PROTOCOL_WEBHOOK
	case domain.ProtocolMQTT:
		return opgatewayv1.Protocol_PROTOCOL_MQTT
	case domain.ProtocolAMQP:
		return opgatewayv1.Protocol_PROTOCOL_AMQP
	default:
		return opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED
	}
}

// protoToDomainProtocol converts a proto Protocol to domain Protocol.
func protoToDomainProtocol(p opgatewayv1.Protocol) domain.Protocol {
	switch p {
	case opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED:
		return ""
	case opgatewayv1.Protocol_PROTOCOL_HTTPS:
		return domain.ProtocolHTTPS
	case opgatewayv1.Protocol_PROTOCOL_GRPC:
		return domain.ProtocolGRPC
	case opgatewayv1.Protocol_PROTOCOL_WEBHOOK:
		return domain.ProtocolWebhook
	case opgatewayv1.Protocol_PROTOCOL_MQTT:
		return domain.ProtocolMQTT
	case opgatewayv1.Protocol_PROTOCOL_AMQP:
		return domain.ProtocolAMQP
	}
	return ""
}

// domainToProtoHealthStatus converts a domain HealthStatus to proto HealthStatus.
func domainToProtoHealthStatus(h domain.HealthStatus) opgatewayv1.HealthStatus {
	switch h {
	case domain.HealthStatusUnknown:
		return opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED
	case domain.HealthStatusHealthy:
		return opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY
	case domain.HealthStatusDegraded:
		return opgatewayv1.HealthStatus_HEALTH_STATUS_DEGRADED
	case domain.HealthStatusUnhealthy:
		return opgatewayv1.HealthStatus_HEALTH_STATUS_UNHEALTHY
	}
	return opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED
}

// ========== Connection/Route Status mappers ==========

// domainToProtoConnectionStatus converts a domain ConnectionStatus to proto ConnectionStatus.
func domainToProtoConnectionStatus(s domain.ConnectionStatus) opgatewayv1.ConnectionStatus {
	switch s {
	case domain.ConnectionStatusActive:
		return opgatewayv1.ConnectionStatus_CONNECTION_STATUS_ACTIVE
	case domain.ConnectionStatusDeprecated:
		return opgatewayv1.ConnectionStatus_CONNECTION_STATUS_DEPRECATED
	default:
		return opgatewayv1.ConnectionStatus_CONNECTION_STATUS_UNSPECIFIED
	}
}

// domainToProtoRouteStatus converts a domain RouteStatus to proto RouteStatus.
func domainToProtoRouteStatus(s domain.RouteStatus) opgatewayv1.RouteStatus {
	switch s {
	case domain.RouteStatusActive:
		return opgatewayv1.RouteStatus_ROUTE_STATUS_ACTIVE
	case domain.RouteStatusDeprecated:
		return opgatewayv1.RouteStatus_ROUTE_STATUS_DEPRECATED
	default:
		return opgatewayv1.RouteStatus_ROUTE_STATUS_UNSPECIFIED
	}
}
