package service

import (
	"testing"

	"github.com/google/uuid"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/structpb"
)

// ========== domainToProtoStatus ==========

func TestDomainToProtoStatus_AllStatuses(t *testing.T) {
	tests := []struct {
		domain domain.InstructionStatus
		proto  opgatewayv1.InstructionStatus
	}{
		{domain.InstructionStatusPending, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING},
		{domain.InstructionStatusDispatching, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING},
		{domain.InstructionStatusDelivered, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED},
		{domain.InstructionStatusAcknowledged, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED},
		{domain.InstructionStatusRetrying, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING},
		{domain.InstructionStatusFailed, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED},
		{domain.InstructionStatusExpired, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED},
		{domain.InstructionStatusCancelled, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.proto, domainToProtoStatus(tt.domain))
		})
	}
}

func TestDomainToProtoStatus_Unknown(t *testing.T) {
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED, domainToProtoStatus("BOGUS"))
}

// ========== protoToDomainStatus ==========

func TestProtoToDomainStatus_AllStatuses(t *testing.T) {
	tests := []struct {
		proto  opgatewayv1.InstructionStatus
		domain domain.InstructionStatus
	}{
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED, ""},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING, domain.InstructionStatusPending},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING, domain.InstructionStatusDispatching},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED, domain.InstructionStatusDelivered},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED, domain.InstructionStatusAcknowledged},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING, domain.InstructionStatusRetrying},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED, domain.InstructionStatusFailed},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED, domain.InstructionStatusExpired},
		{opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED, domain.InstructionStatusCancelled},
	}

	for _, tt := range tests {
		t.Run(tt.proto.String(), func(t *testing.T) {
			assert.Equal(t, tt.domain, protoToDomainStatus(tt.proto))
		})
	}
}

func TestProtoToDomainStatus_Unrecognized(t *testing.T) {
	assert.Equal(t, domain.InstructionStatus(""), protoToDomainStatus(opgatewayv1.InstructionStatus(999)))
}

// ========== domainToProtoPriority ==========

func TestDomainToProtoPriority_AllPriorities(t *testing.T) {
	tests := []struct {
		domain domain.Priority
		proto  opgatewayv1.Priority
	}{
		{domain.PriorityLow, opgatewayv1.Priority_PRIORITY_LOW},
		{domain.PriorityNormal, opgatewayv1.Priority_PRIORITY_NORMAL},
		{domain.PriorityHigh, opgatewayv1.Priority_PRIORITY_HIGH},
		{domain.PriorityCritical, opgatewayv1.Priority_PRIORITY_CRITICAL},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.proto, domainToProtoPriority(tt.domain))
		})
	}
}

func TestDomainToProtoPriority_Unknown(t *testing.T) {
	assert.Equal(t, opgatewayv1.Priority_PRIORITY_NORMAL, domainToProtoPriority("BOGUS"))
}

// ========== protoToDomainPriority ==========

func TestProtoToDomainPriority_AllPriorities(t *testing.T) {
	tests := []struct {
		proto    opgatewayv1.Priority
		expected domain.Priority
	}{
		{opgatewayv1.Priority_PRIORITY_UNSPECIFIED, domain.PriorityNormal},
		{opgatewayv1.Priority_PRIORITY_LOW, domain.PriorityLow},
		{opgatewayv1.Priority_PRIORITY_NORMAL, domain.PriorityNormal},
		{opgatewayv1.Priority_PRIORITY_HIGH, domain.PriorityHigh},
		{opgatewayv1.Priority_PRIORITY_CRITICAL, domain.PriorityCritical},
	}

	for _, tt := range tests {
		t.Run(tt.proto.String(), func(t *testing.T) {
			assert.Equal(t, tt.expected, protoToDomainPriority(tt.proto))
		})
	}
}

func TestProtoToDomainPriority_Unrecognized(t *testing.T) {
	assert.Equal(t, domain.PriorityNormal, protoToDomainPriority(opgatewayv1.Priority(999)))
}

// ========== domainToProtoProtocol ==========

func TestDomainToProtoProtocol_AllProtocols(t *testing.T) {
	tests := []struct {
		domain domain.Protocol
		proto  opgatewayv1.Protocol
	}{
		{domain.ProtocolHTTPS, opgatewayv1.Protocol_PROTOCOL_HTTPS},
		{domain.ProtocolGRPC, opgatewayv1.Protocol_PROTOCOL_GRPC},
		{domain.ProtocolWebhook, opgatewayv1.Protocol_PROTOCOL_WEBHOOK},
		{domain.ProtocolMQTT, opgatewayv1.Protocol_PROTOCOL_MQTT},
		{domain.ProtocolAMQP, opgatewayv1.Protocol_PROTOCOL_AMQP},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.proto, domainToProtoProtocol(tt.domain))
		})
	}
}

func TestDomainToProtoProtocol_Unknown(t *testing.T) {
	assert.Equal(t, opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED, domainToProtoProtocol("BOGUS"))
}

// ========== protoToDomainProtocol ==========

func TestProtoToDomainProtocol_AllProtocols(t *testing.T) {
	tests := []struct {
		proto  opgatewayv1.Protocol
		domain domain.Protocol
	}{
		{opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED, ""},
		{opgatewayv1.Protocol_PROTOCOL_HTTPS, domain.ProtocolHTTPS},
		{opgatewayv1.Protocol_PROTOCOL_GRPC, domain.ProtocolGRPC},
		{opgatewayv1.Protocol_PROTOCOL_WEBHOOK, domain.ProtocolWebhook},
		{opgatewayv1.Protocol_PROTOCOL_MQTT, domain.ProtocolMQTT},
		{opgatewayv1.Protocol_PROTOCOL_AMQP, domain.ProtocolAMQP},
	}

	for _, tt := range tests {
		t.Run(tt.proto.String(), func(t *testing.T) {
			assert.Equal(t, tt.domain, protoToDomainProtocol(tt.proto))
		})
	}
}

func TestProtoToDomainProtocol_Unrecognized(t *testing.T) {
	assert.Equal(t, domain.Protocol(""), protoToDomainProtocol(opgatewayv1.Protocol(999)))
}

// ========== domainToProtoHealthStatus ==========

func TestDomainToProtoHealthStatus_AllStatuses(t *testing.T) {
	tests := []struct {
		domain domain.HealthStatus
		proto  opgatewayv1.HealthStatus
	}{
		{domain.HealthStatusUnknown, opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED},
		{domain.HealthStatusHealthy, opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY},
		{domain.HealthStatusDegraded, opgatewayv1.HealthStatus_HEALTH_STATUS_DEGRADED},
		{domain.HealthStatusUnhealthy, opgatewayv1.HealthStatus_HEALTH_STATUS_UNHEALTHY},
	}

	for _, tt := range tests {
		t.Run(string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.proto, domainToProtoHealthStatus(tt.domain))
		})
	}
}

func TestDomainToProtoHealthStatus_Unknown(t *testing.T) {
	assert.Equal(t, opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED, domainToProtoHealthStatus("BOGUS"))
}

// ========== protoToDomainAuthConfig ==========

func TestProtoToDomainAuthConfig_AllTypes(t *testing.T) {
	t.Run("ApiKey", func(t *testing.T) {
		req := &opgatewayv1.UpsertConnectionRequest{
			AuthConfig: &opgatewayv1.UpsertConnectionRequest_ApiKey{
				ApiKey: &opgatewayv1.ApiKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
			},
		}
		auth := protoToDomainAuthConfig(req)
		apiKey, ok := auth.(*domain.APIKeyAuth)
		assert.True(t, ok)
		assert.Equal(t, "X-Key", apiKey.HeaderName)
		assert.Equal(t, "ref", apiKey.SecretRef)
	})

	t.Run("Basic", func(t *testing.T) {
		req := &opgatewayv1.UpsertConnectionRequest{
			AuthConfig: &opgatewayv1.UpsertConnectionRequest_Basic{
				Basic: &opgatewayv1.BasicAuth{Username: "user", PasswordSecretRef: "pass-ref"},
			},
		}
		auth := protoToDomainAuthConfig(req)
		basic, ok := auth.(*domain.BasicAuth)
		assert.True(t, ok)
		assert.Equal(t, "user", basic.Username)
		assert.Equal(t, "pass-ref", basic.PasswordRef)
	})

	t.Run("OAuth2", func(t *testing.T) {
		req := &opgatewayv1.UpsertConnectionRequest{
			AuthConfig: &opgatewayv1.UpsertConnectionRequest_Oauth2{
				Oauth2: &opgatewayv1.OAuth2Auth{
					TokenUrl:        "https://auth.example.com/token",
					ClientId:        "client-1",
					ClientSecretRef: "secret-ref",
					Scopes:          []string{"read", "write"},
				},
			},
		}
		auth := protoToDomainAuthConfig(req)
		oauth, ok := auth.(*domain.OAuth2Auth)
		assert.True(t, ok)
		assert.Equal(t, "https://auth.example.com/token", oauth.TokenURL)
		assert.Equal(t, "client-1", oauth.ClientID)
		assert.Equal(t, "secret-ref", oauth.ClientSecretRef)
		assert.Equal(t, []string{"read", "write"}, oauth.Scopes)
	})

	t.Run("HMAC", func(t *testing.T) {
		req := &opgatewayv1.UpsertConnectionRequest{
			AuthConfig: &opgatewayv1.UpsertConnectionRequest_Hmac{
				Hmac: &opgatewayv1.HMACAuth{
					Algorithm:       "SHA256",
					SecretRef:       "hmac-ref",
					SignatureHeader: "X-Signature",
				},
			},
		}
		auth := protoToDomainAuthConfig(req)
		hmac, ok := auth.(*domain.HMACAuth)
		assert.True(t, ok)
		assert.Equal(t, "SHA256", hmac.Algorithm)
		assert.Equal(t, "hmac-ref", hmac.SecretRef)
		assert.Equal(t, "X-Signature", hmac.SignatureHeader)
	})

	t.Run("MTLS", func(t *testing.T) {
		req := &opgatewayv1.UpsertConnectionRequest{
			AuthConfig: &opgatewayv1.UpsertConnectionRequest_Mtls{
				Mtls: &opgatewayv1.MTLSAuth{
					ClientCertSecretRef: "cert-ref",
					ClientKeySecretRef:  "key-ref",
					CaCertSecretRef:     "ca-ref",
				},
			},
		}
		auth := protoToDomainAuthConfig(req)
		mtls, ok := auth.(*domain.MTLSAuth)
		assert.True(t, ok)
		assert.Equal(t, "cert-ref", mtls.ClientCertRef)
		assert.Equal(t, "key-ref", mtls.ClientKeyRef)
		assert.Equal(t, "ca-ref", mtls.CACertRef)
	})

	t.Run("NilAuthConfig", func(t *testing.T) {
		req := &opgatewayv1.UpsertConnectionRequest{}
		auth := protoToDomainAuthConfig(req)
		assert.Nil(t, auth)
	})
}

// ========== protoToDomainRetryPolicy ==========

func TestProtoToDomainRetryPolicy(t *testing.T) {
	t.Run("NilProto_ReturnsDefaults", func(t *testing.T) {
		p := protoToDomainRetryPolicy(nil)
		assert.Equal(t, 3, p.MaxAttempts)
		assert.Equal(t, 2.0, p.BackoffMultiplier)
	})

	t.Run("CustomValues", func(t *testing.T) {
		p := protoToDomainRetryPolicy(&opgatewayv1.RetryPolicy{
			MaxAttempts:           5,
			InitialBackoffSeconds: 2,
			MaxBackoffSeconds:     120,
			BackoffMultiplier:     3.0,
		})
		assert.Equal(t, 5, p.MaxAttempts)
		assert.Equal(t, 3.0, p.BackoffMultiplier)
	})

	t.Run("ZeroFields_KeepDefaults", func(t *testing.T) {
		p := protoToDomainRetryPolicy(&opgatewayv1.RetryPolicy{})
		assert.Equal(t, 3, p.MaxAttempts)
	})
}

// ========== protoToDomainRateLimit ==========

func TestProtoToDomainRateLimit(t *testing.T) {
	t.Run("NilProto_ReturnsEmpty", func(t *testing.T) {
		r := protoToDomainRateLimit(nil)
		assert.Equal(t, float64(0), r.RequestsPerSecond)
		assert.Equal(t, 0, r.BurstSize)
	})

	t.Run("ValidValues", func(t *testing.T) {
		r := protoToDomainRateLimit(&opgatewayv1.RateLimit{
			RequestsPerSecond: 100.0,
			BurstSize:         50,
		})
		assert.Equal(t, 100.0, r.RequestsPerSecond)
		assert.Equal(t, 50, r.BurstSize)
	})
}

// ========== anyToProtoValue ==========

func TestAnyToProtoValue_AllTypes(t *testing.T) {
	t.Run("Nil", func(t *testing.T) {
		v := anyToProtoValue(nil)
		assert.NotNil(t, v)
		_, ok := v.GetKind().(*structpb.Value_NullValue)
		assert.True(t, ok)
	})

	t.Run("Bool", func(t *testing.T) {
		v := anyToProtoValue(true)
		assert.True(t, v.GetBoolValue())
	})

	t.Run("Float64", func(t *testing.T) {
		v := anyToProtoValue(float64(3.14))
		assert.InDelta(t, 3.14, v.GetNumberValue(), 0.001)
	})

	t.Run("Float32", func(t *testing.T) {
		v := anyToProtoValue(float32(2.5))
		assert.InDelta(t, 2.5, v.GetNumberValue(), 0.01)
	})

	t.Run("Int", func(t *testing.T) {
		v := anyToProtoValue(42)
		assert.Equal(t, float64(42), v.GetNumberValue())
	})

	t.Run("Int32", func(t *testing.T) {
		v := anyToProtoValue(int32(42))
		assert.Equal(t, float64(42), v.GetNumberValue())
	})

	t.Run("Int64", func(t *testing.T) {
		v := anyToProtoValue(int64(42))
		assert.Equal(t, float64(42), v.GetNumberValue())
	})

	t.Run("Uint", func(t *testing.T) {
		v := anyToProtoValue(uint(42))
		assert.Equal(t, float64(42), v.GetNumberValue())
	})

	t.Run("Uint32", func(t *testing.T) {
		v := anyToProtoValue(uint32(42))
		assert.Equal(t, float64(42), v.GetNumberValue())
	})

	t.Run("Uint64", func(t *testing.T) {
		v := anyToProtoValue(uint64(42))
		assert.Equal(t, float64(42), v.GetNumberValue())
	})

	t.Run("String", func(t *testing.T) {
		v := anyToProtoValue("hello")
		assert.Equal(t, "hello", v.GetStringValue())
	})

	t.Run("SliceAny", func(t *testing.T) {
		v := anyToProtoValue([]any{"a", float64(1)})
		list := v.GetListValue()
		assert.Len(t, list.GetValues(), 2)
		assert.Equal(t, "a", list.GetValues()[0].GetStringValue())
	})

	t.Run("NestedMap", func(t *testing.T) {
		v := anyToProtoValue(map[string]any{"key": "value"})
		s := v.GetStructValue()
		assert.Equal(t, "value", s.Fields["key"].GetStringValue())
	})

	t.Run("UnsupportedType_ReturnsNull", func(t *testing.T) {
		v := anyToProtoValue(struct{}{})
		_, ok := v.GetKind().(*structpb.Value_NullValue)
		assert.True(t, ok)
	})
}

// ========== protoValueToAny ==========

func TestProtoValueToAny_AllTypes(t *testing.T) {
	t.Run("Nil", func(t *testing.T) {
		assert.Nil(t, protoValueToAny(nil))
	})

	t.Run("NullValue", func(t *testing.T) {
		assert.Nil(t, protoValueToAny(structpb.NewNullValue()))
	})

	t.Run("Bool", func(t *testing.T) {
		assert.Equal(t, true, protoValueToAny(structpb.NewBoolValue(true)))
	})

	t.Run("Number", func(t *testing.T) {
		assert.Equal(t, float64(42), protoValueToAny(structpb.NewNumberValue(42)))
	})

	t.Run("String", func(t *testing.T) {
		assert.Equal(t, "hello", protoValueToAny(structpb.NewStringValue("hello")))
	})

	t.Run("List", func(t *testing.T) {
		list := structpb.NewListValue(&structpb.ListValue{
			Values: []*structpb.Value{structpb.NewStringValue("a"), structpb.NewNumberValue(1)},
		})
		result := protoValueToAny(list)
		items, ok := result.([]any)
		assert.True(t, ok)
		assert.Len(t, items, 2)
	})

	t.Run("NilList", func(t *testing.T) {
		v := &structpb.Value{Kind: &structpb.Value_ListValue{ListValue: nil}}
		result := protoValueToAny(v)
		items, ok := result.([]any)
		assert.True(t, ok)
		assert.Empty(t, items)
	})

	t.Run("Struct", func(t *testing.T) {
		s, _ := structpb.NewStruct(map[string]any{"k": "v"})
		v := structpb.NewStructValue(s)
		result := protoValueToAny(v)
		m, ok := result.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "v", m["k"])
	})
}

// ========== mapToStruct / structToMap roundtrip ==========

func TestMapToStruct_NilMap(t *testing.T) {
	assert.Nil(t, mapToStruct(nil))
}

func TestStructToMap_NilStruct(t *testing.T) {
	m := structToMap(nil)
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestStructToMapRoundtrip(t *testing.T) {
	original := map[string]any{
		"str":  "hello",
		"num":  float64(42),
		"bool": true,
	}
	s := mapToStruct(original)
	result := structToMap(s)
	assert.Equal(t, "hello", result["str"])
	assert.Equal(t, float64(42), result["num"])
	assert.Equal(t, true, result["bool"])
}

// ========== connectionToProto ==========

func TestConnectionToProto_AllAuthTypes(t *testing.T) {
	base := func(auth domain.AuthConfig) *domain.ProviderConnection {
		conn, _ := domain.NewProviderConnection(
			testTenantID(), "Provider", "type", domain.ProtocolHTTPS,
			"https://api.example.com", auth,
			domain.RetryPolicy{MaxAttempts: 3}, domain.RateLimitConfig{},
		)
		return conn
	}

	t.Run("OAuth2", func(t *testing.T) {
		conn := base(&domain.OAuth2Auth{
			TokenURL:        "https://auth.example.com/token",
			ClientID:        "c1",
			ClientSecretRef: "cs",
			Scopes:          []string{"read"},
		})
		p := connectionToProto(conn)
		oauth := p.GetOauth2()
		assert.NotNil(t, oauth)
		assert.Equal(t, "https://auth.example.com/token", oauth.TokenUrl)
	})

	t.Run("HMAC", func(t *testing.T) {
		conn := base(&domain.HMACAuth{
			Algorithm:       "SHA256",
			SecretRef:       "ref",
			SignatureHeader: "X-Sig",
		})
		p := connectionToProto(conn)
		hmac := p.GetHmac()
		assert.NotNil(t, hmac)
		assert.Equal(t, "SHA256", hmac.Algorithm)
	})

	t.Run("MTLS", func(t *testing.T) {
		conn := base(&domain.MTLSAuth{
			ClientCertRef: "cert",
			ClientKeyRef:  "key",
			CACertRef:     "ca",
		})
		p := connectionToProto(conn)
		mtls := p.GetMtls()
		assert.NotNil(t, mtls)
		assert.Equal(t, "cert", mtls.ClientCertSecretRef)
	})

	t.Run("BasicAuth", func(t *testing.T) {
		conn := base(&domain.BasicAuth{
			Username:    "user",
			PasswordRef: "pass",
		})
		p := connectionToProto(conn)
		basic := p.GetBasic()
		assert.NotNil(t, basic)
		assert.Equal(t, "user", basic.Username)
	})

	t.Run("WithRateLimit", func(t *testing.T) {
		conn, _ := domain.NewProviderConnection(
			testTenantID(), "Provider", "type", domain.ProtocolHTTPS,
			"https://api.example.com",
			&domain.APIKeyAuth{HeaderName: "X-Key", SecretRef: "ref"},
			domain.RetryPolicy{MaxAttempts: 3},
			domain.RateLimitConfig{RequestsPerSecond: 100, BurstSize: 50},
		)
		p := connectionToProto(conn)
		assert.NotNil(t, p.RateLimit)
		assert.Equal(t, float64(100), p.RateLimit.RequestsPerSecond)
	})
}

// ========== instructionToProto ==========

func TestInstructionToProto_WithAttempts(t *testing.T) {
	inst, err := domain.NewInstruction(
		uuid.MustParse(testTenantID()), "test.cmd", "conn-1",
		map[string]any{"key": "val"},
	)
	assert.NoError(t, err)

	// Add an attempt.
	inst.Attempts = append(inst.Attempts, domain.InstructionAttempt{
		AttemptNumber: 1,
		FailureReason: "timeout",
	})

	p := instructionToProto(inst)
	assert.Len(t, p.Attempts, 1)
	assert.Equal(t, int32(1), p.Attempts[0].AttemptNumber)
	assert.Equal(t, "timeout", p.Attempts[0].ErrorMessage)
}

// ========== attemptToProto ==========

func TestAttemptToProto(t *testing.T) {
	a := domain.InstructionAttempt{
		AttemptNumber: 3,
		FailureReason: "connection refused",
	}
	p := attemptToProto(a)
	assert.Equal(t, int32(3), p.AttemptNumber)
	assert.Equal(t, "connection refused", p.ErrorMessage)
}
