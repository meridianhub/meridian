package persistence

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
)

// Sentinel errors for mapper type assertions.
var (
	ErrUnknownAuthConfigType = errors.New("unknown AuthConfig implementation type")
	ErrUnknownAuthType       = errors.New("unknown auth_type in stored auth config")
)

// instructionToEntity converts a domain Instruction to an InstructionEntity for persistence.
// The idempotency key must be supplied by the caller since it is not part of the domain model.
func instructionToEntity(inst *domain.Instruction, idempotencyKey string) (*InstructionEntity, error) {
	providerConnUUID, err := uuid.Parse(inst.ProviderConnectionID)
	if err != nil {
		return nil, fmt.Errorf("invalid provider_connection_id %q: %w", inst.ProviderConnectionID, err)
	}

	payload := JSONB(inst.Payload)

	var metadata JSONB
	if inst.Metadata != nil {
		metadata = make(JSONB, len(inst.Metadata))
		for k, v := range inst.Metadata {
			metadata[k] = v
		}
	}

	entity := &InstructionEntity{
		ID:                   inst.ID,
		TenantID:             inst.TenantID,
		InstructionType:      inst.InstructionType,
		ProviderConnectionID: providerConnUUID,
		CorrelationID:        nullableString(inst.CorrelationID),
		CausationID:          nullableString(inst.CausationID),
		Payload:              payload,
		Metadata:             metadata,
		Priority:             priorityToInt(string(inst.Priority)),
		Status:               string(inst.Status),
		ScheduledAt:          inst.ScheduledAt,
		ExpiresAt:            inst.ExpiresAt,
		AttemptCount:         inst.AttemptCount,
		MaxAttempts:          inst.MaxAttempts,
		NextRetryAt:          inst.NextRetryAt,
		DispatchedAt:         inst.DispatchedAt,
		CompletedAt:          inst.CompletedAt,
		FailureReason:        nullableString(inst.FailureReason),
		ErrorCode:            nullableString(inst.ErrorCode),
		Version:              inst.Version,
		IdempotencyKey:       idempotencyKey,
		CreatedAt:            inst.CreatedAt,
		UpdatedAt:            inst.UpdatedAt,
	}

	return entity, nil
}

// instructionFromEntity converts an InstructionEntity (plus optional attempts) back to a domain Instruction.
func instructionFromEntity(entity *InstructionEntity, attempts []InstructionAttemptEntity) (*domain.Instruction, error) {
	inst := &domain.Instruction{
		ID:                   entity.ID,
		TenantID:             entity.TenantID,
		InstructionType:      entity.InstructionType,
		ProviderConnectionID: entity.ProviderConnectionID.String(),
		CorrelationID:        derefString(entity.CorrelationID),
		CausationID:          derefString(entity.CausationID),
		Payload:              map[string]any(entity.Payload),
		Priority:             domain.Priority(intToPriority(entity.Priority)),
		Status:               domain.InstructionStatus(entity.Status),
		ScheduledAt:          entity.ScheduledAt,
		ExpiresAt:            entity.ExpiresAt,
		NextRetryAt:          entity.NextRetryAt,
		DispatchedAt:         entity.DispatchedAt,
		CompletedAt:          entity.CompletedAt,
		MaxAttempts:          entity.MaxAttempts,
		AttemptCount:         entity.AttemptCount,
		FailureReason:        derefString(entity.FailureReason),
		ErrorCode:            derefString(entity.ErrorCode),
		Version:              entity.Version,
		CreatedAt:            entity.CreatedAt,
		UpdatedAt:            entity.UpdatedAt,
	}

	// Re-hydrate metadata
	if entity.Metadata != nil {
		meta := make(map[string]string, len(entity.Metadata))
		for k, v := range entity.Metadata {
			if s, ok := v.(string); ok {
				meta[k] = s
			}
		}
		inst.Metadata = meta
	}

	// Re-hydrate attempts
	if len(attempts) > 0 {
		inst.Attempts = make([]domain.InstructionAttempt, len(attempts))
		for i, a := range attempts {
			inst.Attempts[i] = domain.InstructionAttempt{
				AttemptNumber: a.AttemptNumber,
				FailureReason: derefString(a.ErrorMessage),
				ErrorCode:     "", // not stored separately in attempt entity
				AttemptedAt:   a.DispatchedAt,
			}
		}
	} else {
		inst.Attempts = []domain.InstructionAttempt{}
	}

	return inst, nil
}

// connectionToEntity converts a domain ProviderConnection to a ConnectionEntity.
func connectionToEntity(conn *domain.ProviderConnection) (*ConnectionEntity, error) {
	tenantUUID, err := uuid.Parse(conn.TenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id %q: %w", conn.TenantID, err)
	}
	connUUID, err := uuid.Parse(conn.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("invalid connection_id %q: %w", conn.ConnectionID, err)
	}

	authConfig, err := authConfigToJSON(conn.AuthConfig)
	if err != nil {
		return nil, fmt.Errorf("serialize auth_config: %w", err)
	}

	retryPolicy := RetryPolicyJSON{
		MaxAttempts:           conn.RetryPolicy.MaxAttempts,
		InitialBackoffSeconds: conn.RetryPolicy.InitialBackoff.Seconds(),
		MaxBackoffSeconds:     conn.RetryPolicy.MaxBackoff.Seconds(),
		BackoffMultiplier:     conn.RetryPolicy.BackoffMultiplier,
	}

	rateLimitConfig := RateLimitJSON{
		RequestsPerSecond: conn.RateLimitConfig.RequestsPerSecond,
		BurstSize:         conn.RateLimitConfig.BurstSize,
	}

	entity := &ConnectionEntity{
		TenantID:          tenantUUID,
		ConnectionID:      connUUID,
		ProviderName:      conn.ProviderName,
		ProviderType:      conn.ProviderType,
		Protocol:          string(conn.Protocol),
		BaseURL:           conn.BaseURL,
		AuthConfig:        authConfig,
		RetryPolicy:       retryPolicy,
		RateLimitConfig:   rateLimitConfig,
		HealthStatus:      healthStatusForDB(conn.HealthStatus),
		LastHealthCheckAt: conn.LastHealthCheckAt,
		CircuitState:      string(conn.CircuitState),
		CircuitOpenedAt:   conn.CircuitOpenedAt,
		FailureCount:      conn.FailureCount,
		SuccessCount:      conn.SuccessCount,
		CreatedAt:         conn.CreatedAt,
		UpdatedAt:         conn.UpdatedAt,
	}

	return entity, nil
}

// connectionFromEntity converts a ConnectionEntity back to a domain ProviderConnection.
func connectionFromEntity(entity *ConnectionEntity) (*domain.ProviderConnection, error) {
	authConfig, err := authConfigFromJSON(entity.AuthConfig)
	if err != nil {
		return nil, fmt.Errorf("deserialize auth_config: %w", err)
	}

	retryPolicy := domain.RetryPolicy{
		MaxAttempts:       entity.RetryPolicy.MaxAttempts,
		InitialBackoff:    time.Duration(entity.RetryPolicy.InitialBackoffSeconds * float64(time.Second)),
		MaxBackoff:        time.Duration(entity.RetryPolicy.MaxBackoffSeconds * float64(time.Second)),
		BackoffMultiplier: entity.RetryPolicy.BackoffMultiplier,
	}

	rateLimitConfig := domain.RateLimitConfig{
		RequestsPerSecond: entity.RateLimitConfig.RequestsPerSecond,
		BurstSize:         entity.RateLimitConfig.BurstSize,
	}

	conn := &domain.ProviderConnection{
		TenantID:          entity.TenantID.String(),
		ConnectionID:      entity.ConnectionID.String(),
		ProviderName:      entity.ProviderName,
		ProviderType:      entity.ProviderType,
		Protocol:          domain.Protocol(entity.Protocol),
		BaseURL:           entity.BaseURL,
		AuthConfig:        authConfig,
		RetryPolicy:       retryPolicy,
		RateLimitConfig:   rateLimitConfig,
		HealthStatus:      healthStatusFromDB(entity.HealthStatus),
		LastHealthCheckAt: entity.LastHealthCheckAt,
		CircuitState:      domain.CircuitState(entity.CircuitState),
		CircuitOpenedAt:   entity.CircuitOpenedAt,
		FailureCount:      entity.FailureCount,
		SuccessCount:      entity.SuccessCount,
		CreatedAt:         entity.CreatedAt,
		UpdatedAt:         entity.UpdatedAt,
	}

	return conn, nil
}

// authConfigToJSON serializes an AuthConfig interface to the discriminated AuthConfigJSON.
func authConfigToJSON(auth domain.AuthConfig) (AuthConfigJSON, error) {
	switch a := auth.(type) {
	case *domain.APIKeyAuth:
		return AuthConfigJSON{
			AuthType:   a.AuthType(),
			HeaderName: a.HeaderName,
			SecretRef:  a.SecretRef,
		}, nil
	case *domain.BasicAuth:
		return AuthConfigJSON{
			AuthType:    a.AuthType(),
			Username:    a.Username,
			PasswordRef: a.PasswordRef,
		}, nil
	case *domain.OAuth2Auth:
		return AuthConfigJSON{
			AuthType:        a.AuthType(),
			TokenURL:        a.TokenURL,
			ClientID:        a.ClientID,
			ClientSecretRef: a.ClientSecretRef,
			Scopes:          a.Scopes,
		}, nil
	case *domain.HMACAuth:
		return AuthConfigJSON{
			AuthType:        a.AuthType(),
			SecretRef:       a.SecretRef,
			Algorithm:       a.Algorithm,
			SignatureHeader: a.SignatureHeader,
		}, nil
	case *domain.MTLSAuth:
		return AuthConfigJSON{
			AuthType:      a.AuthType(),
			ClientCertRef: a.ClientCertRef,
			ClientKeyRef:  a.ClientKeyRef,
			CACertRef:     a.CACertRef,
		}, nil
	default:
		return AuthConfigJSON{}, fmt.Errorf("%w: %T", ErrUnknownAuthConfigType, auth)
	}
}

// authConfigFromJSON deserializes an AuthConfigJSON to the correct AuthConfig implementation.
func authConfigFromJSON(j AuthConfigJSON) (domain.AuthConfig, error) {
	switch j.AuthType {
	case "api_key":
		return &domain.APIKeyAuth{
			HeaderName: j.HeaderName,
			SecretRef:  j.SecretRef,
		}, nil
	case "basic":
		return &domain.BasicAuth{
			Username:    j.Username,
			PasswordRef: j.PasswordRef,
		}, nil
	case "oauth2":
		return &domain.OAuth2Auth{
			TokenURL:        j.TokenURL,
			ClientID:        j.ClientID,
			ClientSecretRef: j.ClientSecretRef,
			Scopes:          j.Scopes,
		}, nil
	case "hmac":
		return &domain.HMACAuth{
			SecretRef:       j.SecretRef,
			Algorithm:       j.Algorithm,
			SignatureHeader: j.SignatureHeader,
		}, nil
	case "mtls":
		return &domain.MTLSAuth{
			ClientCertRef: j.ClientCertRef,
			ClientKeyRef:  j.ClientKeyRef,
			CACertRef:     j.CACertRef,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownAuthType, j.AuthType)
	}
}

// healthStatusForDB maps domain HealthStatus to the DB CHECK constraint values.
// The DB uses 'UNKNOWN' where domain uses 'UNKNOWN'; other values are identical.
func healthStatusForDB(hs domain.HealthStatus) string {
	switch hs {
	case domain.HealthStatusUnknown:
		return "UNKNOWN"
	case domain.HealthStatusHealthy:
		return "HEALTHY"
	case domain.HealthStatusDegraded:
		return "DEGRADED"
	case domain.HealthStatusUnhealthy:
		return "UNHEALTHY"
	default:
		return "UNKNOWN"
	}
}

// healthStatusFromDB maps a DB health_status string to the domain HealthStatus.
func healthStatusFromDB(s string) domain.HealthStatus {
	switch s {
	case "HEALTHY":
		return domain.HealthStatusHealthy
	case "DEGRADED":
		return domain.HealthStatusDegraded
	case "UNHEALTHY":
		return domain.HealthStatusUnhealthy
	default:
		return domain.HealthStatusUnknown
	}
}
