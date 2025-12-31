# KYC/AML Verification Provider Integration Guide

This guide covers integrating KYC/AML verification providers with Meridian's party service.

## Overview

The verification system uses a provider interface pattern that allows swapping between different
KYC/AML vendors. Currently supported:

| Provider | Status | Use Case |
|----------|--------|----------|
| `mock` | Implemented | Testing and development |
| `jumio` | Planned | Production identity verification |
| `onfido` | Planned | Production identity verification |

## Architecture

```text
                                 +------------------+
   Party Service                 |                  |
   +-------------------+         |  External KYC    |
   |                   |         |  Provider        |
   | VerificationService|-------->|  (Jumio/Onfido) |
   |        |          |         |                  |
   |        v          |         +--------+---------+
   | Provider Interface|                  |
   |        |          |                  |
   |        v          |                  v
   | MockProvider      |         Webhook Callback
   | JumioProvider     |<-----------------+
   | OnfidoProvider    |
   +-------------------+
```

## Configuration

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `VERIFICATION_PROVIDER` | Yes | Provider name: `mock`, `jumio`, or `onfido` |
| `VERIFICATION_WEBHOOK_SECRET` | Non-mock | HMAC secret for webhook signature validation |
| `VERIFICATION_WEBHOOK_URL` | Non-mock | Public URL for provider callbacks |
| `VERIFICATION_API_KEY` | Non-mock | Provider API key |
| `VERIFICATION_API_SECRET` | Non-mock | Provider API secret |
| `VERIFICATION_BASE_URL` | No | Override provider's default API URL |

### Example Configuration

Development (using mock provider):

```bash
export VERIFICATION_PROVIDER=mock
```

Production (using Jumio):

```bash
export VERIFICATION_PROVIDER=jumio
export VERIFICATION_API_KEY=your-api-key
export VERIFICATION_API_SECRET=your-api-secret
export VERIFICATION_WEBHOOK_SECRET=your-webhook-secret
export VERIFICATION_WEBHOOK_URL=https://api.example.com/webhooks/verification/jumio
```

## Adding a New Provider

### Step 1: Implement the Provider Interface

Create a new file `services/party/verification/<provider>_provider.go`:

```go
package verification

import (
    "context"
    "github.com/meridianhub/meridian/services/party/domain"
)

type NewProvider struct {
    apiKey    string
    apiSecret string
    baseURL   string
}

func NewNewProvider(config map[string]string) *NewProvider {
    baseURL := config["base_url"]
    if baseURL == "" {
        baseURL = "https://api.newprovider.com/v1"
    }
    return &NewProvider{
        apiKey:    config["api_key"],
        apiSecret: config["api_secret"],
        baseURL:   baseURL,
    }
}

func (p *NewProvider) VerifyIdentity(ctx context.Context, party *domain.Party) (Result, error) {
    // 1. Call provider's API to initiate verification
    // 2. Return result with VerificationID for tracking
}

func (p *NewProvider) CheckSanctions(ctx context.Context, party *domain.Party) (SanctionsResult, error) {
    // Call provider's sanctions screening API
}

func (p *NewProvider) GetVerificationStatus(ctx context.Context, verificationID string) (Result, error) {
    // Poll provider for current verification status
}
```

### Step 2: Register in Factory

Update `services/party/verification/factory.go`:

```go
func NewProvider(cfg *config.VerificationConfig) (Provider, error) {
    switch strings.ToLower(cfg.Provider) {
    case "mock":
        return NewMockProvider().WithAlwaysApprove(true), nil
    case "newprovider":
        return NewNewProvider(cfg.ProviderConfig), nil
    // ... other providers
    }
}
```

### Step 3: Update Configuration Validation

Add the new provider to `services/party/config/verification.go`:

```go
var SupportedProviders = []string{"mock", "jumio", "onfido", "newprovider"}
```

### Step 4: Add Contract Tests

Create provider-specific contract tests that verify the implementation correctly handles:

- Successful verification flows
- Rejection scenarios
- Error handling
- Webhook payload parsing

## Webhook Security

Webhooks are secured using HMAC-SHA256 signatures. The provider signs the request body and
includes the signature in the `X-Webhook-Signature` header.

### Signature Validation

```go
// The webhook handler validates:
// 1. Signature is present in X-Webhook-Signature header
// 2. Signature matches HMAC-SHA256(body, secret)
// 3. Timestamp is within acceptable window (prevents replay attacks)
```

### Replay Attack Prevention

The webhook handler enforces timestamp freshness:

- Webhooks older than 5 minutes are rejected
- Webhooks more than 30 seconds in the future are rejected (clock drift tolerance)

### Best Practices

1. **Use strong secrets**: Generate webhook secrets with at least 32 bytes of entropy
2. **Rotate secrets periodically**: Update webhook secrets quarterly
3. **Use HTTPS**: Always configure webhook URLs with HTTPS
4. **Log validation failures**: Monitor for signature validation failures as potential attacks
5. **Implement idempotency**: The system returns 200 OK for duplicate webhook deliveries

### Generating a Webhook Secret

```bash
# Generate a secure random secret
openssl rand -hex 32
```

## Testing Strategy

### Unit Tests

Test individual provider implementations in isolation:

```go
func TestNewProvider_VerifyIdentity(t *testing.T) {
    provider := NewNewProvider(map[string]string{
        "api_key":    "test-key",
        "api_secret": "test-secret",
    })

    party := createTestParty(t)
    result, err := provider.VerifyIdentity(context.Background(), party)

    require.NoError(t, err)
    assert.NotEmpty(t, result.VerificationID)
}
```

### Integration Tests with Mock Provider

Use the mock provider for end-to-end flow testing:

```go
func TestKYCFlow_EndToEnd(t *testing.T) {
    // 1. Configure mock provider
    provider := NewMockProvider().WithAlwaysApprove(true)

    // 2. Initiate verification
    result, err := provider.VerifyIdentity(ctx, party)
    require.NoError(t, err)

    // 3. Simulate webhook callback
    // 4. Verify status updated correctly
}
```

### Contract Tests

Verify that provider implementations handle the same scenarios consistently:

```go
func TestProviderContract(t *testing.T) {
    providers := []struct {
        name     string
        provider Provider
    }{
        {"mock", NewMockProvider()},
        // Add other providers as implemented
    }

    for _, tc := range providers {
        t.Run(tc.name, func(t *testing.T) {
            // Run contract tests
        })
    }
}
```

### Webhook Security Tests

Test scenarios to ensure security measures work:

```go
func TestWebhookSecurity(t *testing.T) {
    t.Run("invalid signature rejected", func(t *testing.T) { ... })
    t.Run("missing signature rejected", func(t *testing.T) { ... })
    t.Run("expired timestamp rejected", func(t *testing.T) { ... })
    t.Run("future timestamp rejected", func(t *testing.T) { ... })
    t.Run("replay attack prevented", func(t *testing.T) { ... })
}
```

## Monitoring and Observability

### Key Metrics

| Metric | Description |
|--------|-------------|
| `verification_initiated_total` | Count of verifications started |
| `verification_completed_total` | Count of verifications completed (by status) |
| `verification_duration_seconds` | Time from initiation to completion |
| `webhook_received_total` | Count of webhooks received (by provider) |
| `webhook_validation_failures_total` | Count of failed webhook validations |

### Logging

The verification service logs:

- Verification initiation with party ID and provider
- Webhook receipt with verification ID and status
- Signature validation failures (warning level)
- Status updates with old and new status

## Error Handling

### Provider Errors

| Error | HTTP Status | Retry? | Description |
|-------|-------------|--------|-------------|
| Rate limited | 429 | Yes | Back off and retry |
| Unauthorized | 401 | No | Check API credentials |
| Bad request | 400 | No | Fix request payload |
| Server error | 500 | Yes | Provider issue, retry with backoff |

### Webhook Processing Errors

| Error | HTTP Status | Action |
|-------|-------------|--------|
| Invalid signature | 401 | Log as potential attack |
| Verification not found | 404 | Check if verification was initiated |
| Already completed | 200 | Idempotent, return success |
| Service error | 500 | Provider will retry |

## Multi-Tenancy

The verification system is multi-tenant aware:

- Each verification is associated with a party in a specific tenant
- Database queries are scoped to the tenant's schema
- Webhook handlers extract tenant context from the verification record

## Migration Between Providers

When migrating from one provider to another:

1. **Parallel mode**: Configure both providers temporarily
2. **New verifications**: Route to new provider
3. **Existing verifications**: Continue processing on old provider
4. **Cutover**: Once all old verifications complete, remove old provider config

The provider factory pattern makes this straightforward as providers share the same interface.
