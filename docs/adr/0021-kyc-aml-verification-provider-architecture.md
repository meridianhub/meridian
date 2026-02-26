---
name: adr-021-kyc-aml-verification-provider-architecture
description: Architecture for external KYC/AML verification provider integration with async flows, multi-provider support, and privacy-first design
triggers:

  - Integrating with KYC or AML verification providers
  - Designing identity verification workflows
  - Implementing webhook handling for verification callbacks
  - Managing PII data for compliance verification
  - Adding new verification providers to the platform

instructions: |
  Use async verification as default for KYC/AML checks (90%+ of cases). Implement provider
  abstraction via VerificationProvider interface. Store only verification results and
  references, never raw PII. All webhooks require HMAC signature validation and idempotency.
---

# 21. KYC/AML Verification Provider Architecture

Date: 2025-12-31

## Status

Accepted

## Context

Meridian requires integration with external Know Your Customer (KYC) and Anti-Money Laundering (AML) verification providers to comply with financial regulations. These providers perform identity verification, document checks, sanctions screening, and risk assessments.

Key challenges include:

* **Verification latency**: Provider checks range from milliseconds (sanctions screening) to days (manual document review)
* **Provider diversity**: Different jurisdictions require different providers (e.g., Onfido for UK, Jumio for EU, Persona for US)
* **Data sensitivity**: PII must be handled according to GDPR, CCPA, and financial services regulations
* **Reliability**: Verification flows must handle provider outages without blocking customer onboarding
* **Audit requirements**: Complete audit trail for regulatory reporting and dispute resolution

The system must support both synchronous checks (instant sanctions screening) and asynchronous flows (document verification with manual review).

## Decision Drivers

* **Regulatory compliance**: Meet KYC/AML requirements across multiple jurisdictions
* **Provider flexibility**: Support multiple providers without code changes
* **Privacy by design**: Minimise PII exposure and retention
* **Resilience**: Handle provider failures gracefully
* **Audit trail**: Complete traceability for regulatory examinations
* **Latency tolerance**: Accept appropriate delays for thorough verification
* **Cost optimisation**: Route to cost-effective providers when equivalent

## Considered Options

1. **Async-first with synchronous fallback** via provider abstraction layer
2. **Synchronous-only** with timeout-based fallbacks
3. **Event-sourced verification saga** with eventual consistency
4. **Third-party orchestration** (e.g., Alloy, Unit21)

## Decision Outcome

Chosen option: **"Async-first with synchronous fallback via provider abstraction layer"**, because:

* Matches real-world verification timelines (hours to days for document review)
* Provider interface abstraction enables multi-provider support without code changes
* Webhook-based callbacks align with industry standard (all major providers use webhooks)
* Synchronous fallback available for instant checks (sanctions screening)
* Privacy controls enforced at the abstraction layer

### Positive Consequences

* **Provider agnostic**: Add new providers by implementing interface
* **Graceful degradation**: Provider outages don't block entire flow
* **Cost optimisation**: Route to cheaper providers for low-risk checks
* **Compliance ready**: Audit logs capture complete verification history
* **Privacy enforced**: PII handling rules centralised in abstraction layer

### Negative Consequences

* **Increased complexity**: Webhook infrastructure requires additional components
* **Eventual consistency**: Verification status may lag behind provider state
* **Testing difficulty**: Async flows harder to test end-to-end
* **Operational overhead**: Webhook endpoint requires monitoring and alerting

## Pros and Cons of the Options

### Option 1: Async-First with Synchronous Fallback (Chosen)

Provider abstraction layer with default async verification and optional sync path.

* Good, because matches provider API patterns (webhook-based)
* Good, because supports long-running verifications (document review)
* Good, because provider abstraction enables multi-provider routing
* Good, because sync fallback available for instant checks
* Bad, because requires webhook infrastructure
* Bad, because async flows increase system complexity

### Option 2: Synchronous-Only

All verification calls block until completion with timeout.

* Good, because simpler request-response flow
* Good, because immediate verification result
* Bad, because provider timeouts (30s+) block user experience
* Bad, because cannot support manual review workflows
* Bad, because poor resilience when providers are slow

### Option 3: Event-Sourced Verification Saga

Full event sourcing with verification events and projections.

* Good, because complete audit trail via event log
* Good, because supports complex multi-step verification
* Bad, because significant infrastructure complexity
* Bad, because overkill for current verification requirements
* Bad, because steep learning curve for team

### Option 4: Third-Party Orchestration (Alloy, Unit21)

Delegate verification orchestration to specialised platform.

* Good, because pre-built provider integrations
* Good, because compliance expertise built-in
* Bad, because vendor lock-in
* Bad, because additional cost layer
* Bad, because less control over verification flow

## Architecture Overview

```text
┌─────────────────────────────────────────────────────────────────────────┐
│                        Party Directory Service                          │
│                  (BIAN Party Directory domain owner)                    │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    Verification Orchestrator                            │
│  ┌─────────────────┐  ┌──────────────────┐  ┌────────────────────────┐  │
│  │ Request Router  │  │ Status Tracker   │  │ Result Aggregator      │  │
│  │ (provider       │  │ (verification    │  │ (combine multi-        │  │
│  │  selection)     │  │  state machine)  │  │  provider results)     │  │
│  └─────────────────┘  └──────────────────┘  └────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
          │                         │                         │
          ▼                         ▼                         ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    Provider Abstraction Layer                           │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    VerificationProvider Interface                │   │
│  │  - InitiateVerification(request) -> VerificationReference        │   │
│  │  - CheckStatus(reference) -> VerificationStatus                  │   │
│  │  - ParseWebhook(payload, signature) -> VerificationResult        │   │
│  │  - GetSupportedChecks() -> []CheckType                           │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│          ▲                    ▲                    ▲                     │
│          │                    │                    │                     │
│  ┌───────┴───────┐   ┌───────┴───────┐   ┌───────┴───────┐             │
│  │ Onfido        │   │ Jumio         │   │ ComplyAdvantage│             │
│  │ Adapter       │   │ Adapter       │   │ Adapter        │             │
│  └───────────────┘   └───────────────┘   └───────────────┘             │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                       Webhook Handler                                   │
│  ┌─────────────────┐  ┌──────────────────┐  ┌────────────────────────┐  │
│  │ Signature       │  │ Idempotency      │  │ Event Publisher        │  │
│  │ Validator       │  │ Guard            │  │ (Kafka)                │  │
│  └─────────────────┘  └──────────────────┘  └────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
```

## Implementation Details

### 1. Synchronous vs Asynchronous Verification Flows

#### When to Use Synchronous (Blocking)

Use synchronous verification for checks that:
* Complete within 5 seconds (configurable timeout)
* Are required before proceeding (hard blockers)
* Have high availability SLAs from providers

| Check Type | Typical Latency | Flow |
|------------|-----------------|------|
| Sanctions screening | 100-500ms | Synchronous |
| PEP (Politically Exposed Persons) | 200-800ms | Synchronous |
| Watchlist screening | 100-300ms | Synchronous |

```go
// Synchronous verification - blocks until result
func (s *VerificationService) ScreenSanctions(ctx context.Context, req *SanctionsRequest) (*SanctionsResult, error) {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    provider := s.router.SelectProvider(CheckTypeSanctions, req.Jurisdiction)
    return provider.ScreenSanctions(ctx, req)
}
```

#### When to Use Asynchronous (Non-Blocking)

Use asynchronous verification for checks that:
* May require manual review
* Have unpredictable latency (seconds to days)
* Can proceed with provisional status

| Check Type | Typical Latency | Flow |
|------------|-----------------|------|
| Document verification | 1-10 minutes | Async |
| Identity verification (selfie) | 30 seconds - 5 minutes | Async |
| Address verification | 1-24 hours | Async |
| Manual review escalation | 1-72 hours | Async |
| Enhanced due diligence | Days to weeks | Async |

```go
// Asynchronous verification - returns reference immediately
func (s *VerificationService) InitiateIdentityVerification(
    ctx context.Context,
    req *IdentityVerificationRequest,
) (*VerificationReference, error) {
    provider := s.router.SelectProvider(CheckTypeIdentity, req.Jurisdiction)

    ref, err := provider.InitiateVerification(ctx, req)
    if err != nil {
        return nil, err
    }

    // Store pending verification
    verification := &Verification{
        ID:            uuid.New(),
        PartyID:       req.PartyID,
        ProviderRef:   ref.ProviderReference,
        Provider:      provider.Name(),
        CheckType:     CheckTypeIdentity,
        Status:        VerificationStatusPending,
        InitiatedAt:   time.Now(),
    }

    if err := s.repo.Save(ctx, verification); err != nil {
        return nil, err
    }

    // Publish event for downstream consumers
    s.publisher.PublishVerificationInitiated(ctx, verification)

    return &VerificationReference{
        VerificationID: verification.ID,
        ProviderRef:    ref.ProviderReference,
        CheckURL:       ref.CheckURL,  // URL for user to complete verification
    }, nil
}
```

### 2. Provider Selection Strategy

#### Multi-Provider Architecture

```go
type ProviderRouter struct {
    providers  map[string]VerificationProvider
    rules      []RoutingRule
    fallbacks  map[string][]string  // provider -> fallback providers
}

type RoutingRule struct {
    CheckType    CheckType
    Jurisdiction string
    RiskLevel    RiskLevel
    Provider     string
    Priority     int
}

func (r *ProviderRouter) SelectProvider(
    checkType CheckType,
    jurisdiction string,
    riskLevel RiskLevel,
) (VerificationProvider, error) {
    // Find matching rules sorted by priority
    candidates := r.findMatchingRules(checkType, jurisdiction, riskLevel)

    for _, rule := range candidates {
        provider := r.providers[rule.Provider]
        if provider.IsHealthy() {
            return provider, nil
        }
    }

    return nil, ErrNoAvailableProvider
}
```

#### Provider Failover

```go
func (r *ProviderRouter) ExecuteWithFailover(
    ctx context.Context,
    checkType CheckType,
    jurisdiction string,
    fn func(VerificationProvider) error,
) error {
    primaryProvider, _ := r.SelectProvider(checkType, jurisdiction, RiskLevelNormal)

    err := fn(primaryProvider)
    if err == nil {
        return nil
    }

    // Try fallback providers
    fallbacks := r.fallbacks[primaryProvider.Name()]
    for _, fallbackName := range fallbacks {
        fallback := r.providers[fallbackName]
        if fallback.IsHealthy() {
            if err := fn(fallback); err == nil {
                return nil
            }
        }
    }

    return fmt.Errorf("all providers failed for %s: %w", checkType, err)
}
```

#### Routing Configuration

```yaml
verification:
  providers:
    onfido:
      api_key: ${ONFIDO_API_KEY}
      webhook_secret: ${ONFIDO_WEBHOOK_SECRET}
      supported_checks: [identity, document, address]
      jurisdictions: [GB, EU]

    jumio:
      api_key: ${JUMIO_API_KEY}
      webhook_secret: ${JUMIO_WEBHOOK_SECRET}
      supported_checks: [identity, document]
      jurisdictions: [US, CA]

    complyadvantage:
      api_key: ${COMPLYADVANTAGE_API_KEY}
      webhook_secret: ${COMPLYADVANTAGE_WEBHOOK_SECRET}
      supported_checks: [sanctions, pep, watchlist]
      jurisdictions: [GLOBAL]

  routing_rules:
    - check_type: identity
      jurisdiction: GB
      provider: onfido
      priority: 1

    - check_type: identity
      jurisdiction: GB
      provider: jumio
      priority: 2  # Fallback

    - check_type: sanctions
      jurisdiction: "*"
      provider: complyadvantage
      priority: 1
```

### 3. Data Retention and GDPR Compliance

#### Data Classification

| Data Type | Retention Period | Storage | Encryption |
|-----------|------------------|---------|------------|
| Verification result (pass/fail) | 7 years | Database | At-rest (AES-256) |
| Provider reference ID | 7 years | Database | At-rest |
| Risk score | 7 years | Database | At-rest |
| Document images | Not stored | Provider only | N/A |
| Selfie images | Not stored | Provider only | N/A |
| Raw PII (DOB, address) | Not stored | Provider only | N/A |
| Verification audit log | 7 years | Audit service | At-rest |

#### What We Store (Minimal)

```go
type VerificationRecord struct {
    ID                uuid.UUID          `gorm:"type:uuid;primaryKey"`
    PartyID           uuid.UUID          `gorm:"type:uuid;not null;index"`
    ProviderReference string             `gorm:"size:255;not null"`  // Provider's ID
    Provider          string             `gorm:"size:50;not null"`
    CheckType         CheckType          `gorm:"size:50;not null"`
    Status            VerificationStatus `gorm:"size:50;not null"`
    RiskScore         *int               `gorm:""`                   // 0-100, nullable
    RiskLevel         *RiskLevel         `gorm:"size:20"`            // LOW, MEDIUM, HIGH
    ResultSummary     string             `gorm:"size:500"`           // "Document verified", "Sanctions match found"
    FailureReason     *string            `gorm:"size:500"`           // If failed
    InitiatedAt       time.Time          `gorm:"not null"`
    CompletedAt       *time.Time         `gorm:""`
    ExpiresAt         *time.Time         `gorm:"index"`              // When verification expires
    CreatedAt         time.Time          `gorm:"not null"`
    UpdatedAt         time.Time          `gorm:"not null"`
}
```

#### What We Do NOT Store

* Raw document images (passports, driving licenses)
* Selfie/biometric images
* Full address details
* Date of birth (beyond age verification result)
* Social security numbers / national ID numbers
* Bank statements or proof of address documents

#### GDPR Data Subject Rights Implementation

```go
// Right to Access - Return verification history without raw PII
func (s *VerificationService) GetVerificationHistory(
    ctx context.Context,
    partyID uuid.UUID,
) ([]*VerificationSummary, error) {
    records, err := s.repo.FindByPartyID(ctx, partyID)
    if err != nil {
        return nil, err
    }

    summaries := make([]*VerificationSummary, len(records))
    for i, r := range records {
        summaries[i] = &VerificationSummary{
            CheckType:     r.CheckType,
            Status:        r.Status,
            ResultSummary: r.ResultSummary,
            CompletedAt:   r.CompletedAt,
            // No PII included
        }
    }
    return summaries, nil
}

// Right to Erasure - Delete verification records (after retention period)
func (s *VerificationService) DeletePartyVerifications(
    ctx context.Context,
    partyID uuid.UUID,
) error {
    // Verify retention period has passed
    records, err := s.repo.FindByPartyID(ctx, partyID)
    if err != nil {
        return err
    }

    for _, r := range records {
        if !r.RetentionPeriodExpired() {
            return fmt.Errorf("retention period not expired for verification %s", r.ID)
        }
    }

    // Also request deletion from provider
    for _, r := range records {
        provider := s.router.GetProvider(r.Provider)
        if err := provider.RequestDataDeletion(ctx, r.ProviderReference); err != nil {
            logger.Warn("Failed to delete from provider", "provider", r.Provider, "error", err)
            // Continue - provider deletion is best-effort
        }
    }

    return s.repo.DeleteByPartyID(ctx, partyID)
}
```

### 4. Privacy Requirements and PII Handling

#### Encryption at Rest

All verification data encrypted using AES-256-GCM:

```go
type EncryptedVerificationStore struct {
    repo      VerificationRepository
    encryptor Encryptor
}

func (s *EncryptedVerificationStore) Save(ctx context.Context, v *Verification) error {
    // Encrypt sensitive fields before storage
    encrypted := &VerificationEntity{
        ID:                v.ID,
        PartyID:           v.PartyID,
        Provider:          v.Provider,
        ProviderReference: s.encryptor.Encrypt(v.ProviderReference),  // Encrypted
        Status:            v.Status,
        ResultSummary:     s.encryptor.Encrypt(v.ResultSummary),      // Encrypted
        // ... other fields
    }
    return s.repo.Save(ctx, encrypted)
}
```

#### PII Minimization Principle

```go
// Request only necessary data from provider
type IdentityVerificationRequest struct {
    PartyID         uuid.UUID
    // Minimal PII - only what's required for verification
    FirstName       string  // Required for name matching
    LastName        string  // Required for name matching
    CountryOfIssue  string  // Required for document routing
    DocumentType    string  // passport, driving_license, national_id
    // NOT included: full address, phone, email, SSN
}

// Provider handles PII collection directly
type VerificationReference struct {
    VerificationID uuid.UUID
    ProviderRef    string
    CheckURL       string  // User completes verification directly with provider
    ExpiresAt      time.Time
}
```

#### Audit Logging for PII Access

```go
func (s *VerificationService) GetVerificationDetails(
    ctx context.Context,
    verificationID uuid.UUID,
) (*VerificationDetails, error) {
    // Log access for audit trail
    s.auditLogger.LogAccess(ctx, AuditEvent{
        Action:       "verification.view",
        ResourceType: "verification",
        ResourceID:   verificationID.String(),
        Actor:        GetActorFromContext(ctx),
        Reason:       GetAccessReasonFromContext(ctx),
        Timestamp:    time.Now(),
    })

    return s.repo.FindByID(ctx, verificationID)
}
```

### 5. Webhook Callback Handling

#### Security Requirements

```go
type WebhookHandler struct {
    providers      map[string]VerificationProvider
    idempotencyStore IdempotencyStore
    publisher      EventPublisher
}

func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
    provider := r.URL.Query().Get("provider")
    if provider == "" {
        http.Error(w, "missing provider", http.StatusBadRequest)
        return
    }

    // 1. Validate signature
    verifier, ok := h.providers[provider]
    if !ok {
        http.Error(w, "unknown provider", http.StatusBadRequest)
        return
    }

    body, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "failed to read body", http.StatusBadRequest)
        return
    }

    signature := r.Header.Get("X-Webhook-Signature")
    if !verifier.ValidateSignature(body, signature) {
        logger.Warn("Invalid webhook signature", "provider", provider)
        http.Error(w, "invalid signature", http.StatusUnauthorized)
        return
    }

    // 2. Parse webhook payload
    result, err := verifier.ParseWebhook(body)
    if err != nil {
        http.Error(w, "failed to parse webhook", http.StatusBadRequest)
        return
    }

    // 3. Idempotency check
    idempotencyKey := fmt.Sprintf("%s:%s", provider, result.EventID)
    if h.idempotencyStore.HasProcessed(r.Context(), idempotencyKey) {
        // Already processed - return success (idempotent)
        w.WriteHeader(http.StatusOK)
        return
    }

    // 4. Process webhook
    if err := h.processWebhook(r.Context(), provider, result); err != nil {
        logger.Error("Failed to process webhook", "error", err)
        http.Error(w, "processing failed", http.StatusInternalServerError)
        return
    }

    // 5. Mark as processed
    h.idempotencyStore.MarkProcessed(r.Context(), idempotencyKey, 24*time.Hour)

    w.WriteHeader(http.StatusOK)
}
```

#### HMAC Signature Validation

```go
type OnfidoProvider struct {
    webhookSecret []byte
}

func (p *OnfidoProvider) ValidateSignature(payload []byte, signature string) bool {
    mac := hmac.New(sha256.New, p.webhookSecret)
    mac.Write(payload)
    expectedMAC := mac.Sum(nil)
    expectedSignature := hex.EncodeToString(expectedMAC)

    return hmac.Equal([]byte(signature), []byte(expectedSignature))
}
```

#### Retry Handling

Webhook endpoints must be idempotent because providers retry on failure:

```go
type IdempotencyStore interface {
    HasProcessed(ctx context.Context, key string) bool
    MarkProcessed(ctx context.Context, key string, ttl time.Duration) error
}

type RedisIdempotencyStore struct {
    client *redis.Client
}

func (s *RedisIdempotencyStore) HasProcessed(ctx context.Context, key string) bool {
    exists, _ := s.client.Exists(ctx, "webhook:processed:"+key).Result()
    return exists > 0
}

func (s *RedisIdempotencyStore) MarkProcessed(ctx context.Context, key string, ttl time.Duration) error {
    return s.client.Set(ctx, "webhook:processed:"+key, "1", ttl).Err()
}
```

#### Webhook Event Processing

```go
func (h *WebhookHandler) processWebhook(
    ctx context.Context,
    provider string,
    result *WebhookResult,
) error {
    // Find verification by provider reference
    verification, err := h.repo.FindByProviderReference(ctx, provider, result.Reference)
    if err != nil {
        return fmt.Errorf("verification not found: %w", err)
    }

    // Update verification status
    verification.Status = result.Status
    verification.RiskScore = result.RiskScore
    verification.RiskLevel = result.RiskLevel
    verification.ResultSummary = result.Summary
    verification.CompletedAt = &result.CompletedAt

    if result.Status == VerificationStatusFailed {
        verification.FailureReason = &result.FailureReason
    }

    if err := h.repo.Update(ctx, verification); err != nil {
        return fmt.Errorf("failed to update verification: %w", err)
    }

    // Publish domain event
    event := &VerificationCompleted{
        VerificationID: verification.ID,
        PartyID:        verification.PartyID,
        CheckType:      verification.CheckType,
        Status:         verification.Status,
        RiskLevel:      verification.RiskLevel,
        CompletedAt:    *verification.CompletedAt,
    }

    return h.publisher.Publish(ctx, "verification.completed", event)
}
```

### 6. Provider Abstraction Design

#### Core Interface

```go
// VerificationProvider defines the contract for all KYC/AML providers
type VerificationProvider interface {
    // Provider identification
    Name() string
    SupportedChecks() []CheckType
    SupportedJurisdictions() []string

    // Health check
    IsHealthy() bool
    HealthCheck(ctx context.Context) error

    // Synchronous operations
    ScreenSanctions(ctx context.Context, req *SanctionsRequest) (*SanctionsResult, error)
    ScreenPEP(ctx context.Context, req *PEPRequest) (*PEPResult, error)

    // Asynchronous operations
    InitiateVerification(ctx context.Context, req *VerificationRequest) (*VerificationReference, error)
    CheckStatus(ctx context.Context, reference string) (*VerificationStatus, error)

    // Webhook handling
    ValidateSignature(payload []byte, signature string) bool
    ParseWebhook(payload []byte) (*WebhookResult, error)

    // GDPR compliance
    RequestDataDeletion(ctx context.Context, reference string) error
}
```

#### Adding a New Provider

1. **Create adapter implementing interface:**

```go
// internal/adapters/verification/persona_provider.go
type PersonaProvider struct {
    client        *persona.Client
    webhookSecret []byte
    config        PersonaConfig
}

var _ VerificationProvider = (*PersonaProvider)(nil)  // Compile-time check

func NewPersonaProvider(config PersonaConfig) *PersonaProvider {
    return &PersonaProvider{
        client:        persona.NewClient(config.APIKey),
        webhookSecret: []byte(config.WebhookSecret),
        config:        config,
    }
}

func (p *PersonaProvider) Name() string {
    return "persona"
}

func (p *PersonaProvider) SupportedChecks() []CheckType {
    return []CheckType{CheckTypeIdentity, CheckTypeDocument}
}

func (p *PersonaProvider) SupportedJurisdictions() []string {
    return []string{"US", "CA"}
}

// Implement remaining interface methods...
```

2. **Register in provider factory:**

```go
func NewProviderRouter(config *Config) *ProviderRouter {
    router := &ProviderRouter{
        providers: make(map[string]VerificationProvider),
    }

    // Register providers based on configuration
    if config.Onfido.Enabled {
        router.providers["onfido"] = NewOnfidoProvider(config.Onfido)
    }
    if config.Jumio.Enabled {
        router.providers["jumio"] = NewJumioProvider(config.Jumio)
    }
    if config.Persona.Enabled {
        router.providers["persona"] = NewPersonaProvider(config.Persona)
    }
    if config.ComplyAdvantage.Enabled {
        router.providers["complyadvantage"] = NewComplyAdvantageProvider(config.ComplyAdvantage)
    }

    router.loadRoutingRules(config.RoutingRules)
    return router
}
```

3. **Add webhook route:**

```go
func SetupWebhookRoutes(router *mux.Router, handler *WebhookHandler) {
    // Single endpoint with provider query param
    router.HandleFunc("/webhooks/verification", handler.HandleWebhook).
        Methods("POST").
        Queries("provider", "{provider}")

    // Or provider-specific endpoints
    router.HandleFunc("/webhooks/onfido", handler.HandleOnfidoWebhook).Methods("POST")
    router.HandleFunc("/webhooks/jumio", handler.HandleJumioWebhook).Methods("POST")
    router.HandleFunc("/webhooks/persona", handler.HandlePersonaWebhook).Methods("POST")
}
```

#### Testing New Providers

```go
func TestPersonaProvider_ImplementsInterface(t *testing.T) {
    // Verify all interface methods are implemented
    var _ VerificationProvider = (*PersonaProvider)(nil)
}

func TestPersonaProvider_InitiateVerification(t *testing.T) {
    // Use provider's sandbox/test environment
    provider := NewPersonaProvider(PersonaConfig{
        APIKey:    os.Getenv("PERSONA_TEST_API_KEY"),
        Sandbox:   true,
    })

    ref, err := provider.InitiateVerification(context.Background(), &VerificationRequest{
        FirstName:      "Test",
        LastName:       "User",
        CountryOfIssue: "US",
        DocumentType:   "driving_license",
    })

    require.NoError(t, err)
    assert.NotEmpty(t, ref.ProviderReference)
    assert.NotEmpty(t, ref.CheckURL)
}

func TestPersonaProvider_WebhookSignature(t *testing.T) {
    provider := NewPersonaProvider(PersonaConfig{
        WebhookSecret: "test-secret",
    })

    payload := []byte(`{"event_type":"verification.completed"}`)

    // Create valid signature
    mac := hmac.New(sha256.New, []byte("test-secret"))
    mac.Write(payload)
    validSignature := hex.EncodeToString(mac.Sum(nil))

    assert.True(t, provider.ValidateSignature(payload, validSignature))
    assert.False(t, provider.ValidateSignature(payload, "invalid-signature"))
}
```

## Verification Status State Machine

```text
                    ┌──────────────┐
                    │   PENDING    │
                    └──────────────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
     ┌──────────────┐ ┌──────────┐ ┌──────────────┐
     │ IN_PROGRESS  │ │  FAILED  │ │   EXPIRED    │
     └──────────────┘ └──────────┘ └──────────────┘
              │            ▲
              │            │
              ▼            │
     ┌──────────────┐      │
     │    REVIEW    │──────┘
     └──────────────┘
              │
              │
              ▼
     ┌──────────────┐
     │   APPROVED   │
     └──────────────┘
```

## Links

* [ADR-002: Microservices per BIAN Domain](./0002-microservices-per-bian-domain.md)
* [ADR-004: Event Schema Evolution](./0004-event-schema-evolution.md)
* [ADR-005: Adapter Pattern for Layer Translation](./0005-adapter-pattern-layer-translation.md)
* [ADR-009: Application-Level Audit Logging](./0009-application-level-audit-logging.md)
* [ADR-019: Resilient Client Patterns](./0019-resilient-client-patterns.md)
* [BIAN Party Directory Service Domain](https://bian.org/servicelandscape-14-0-0/views/view_464.html)
* [GDPR Article 17 - Right to Erasure](https://gdpr-info.eu/art-17-gdpr/)
* [PCI DSS Requirements for PII](https://www.pcisecuritystandards.org/)

## Notes

### Future Considerations

* **Biometric liveness detection**: As deepfake technology improves, consider providers with advanced liveness detection
* **Continuous monitoring**: Move from point-in-time verification to ongoing sanctions monitoring
* **Self-sovereign identity**: SSI/DID integration when ecosystem matures
* **Provider SLA monitoring**: Automated provider health scoring based on latency and accuracy

### Migration Strategy

Existing identity verification implementations should migrate to this architecture:

1. Implement provider interface for existing provider
2. Add routing rules
3. Migrate existing verification records to new schema
4. Deprecate direct provider calls
5. Remove legacy implementation

### Operational Considerations

* **Webhook endpoint availability**: Must have 99.9%+ uptime (providers retry, but queue eventually)
* **Monitoring**: Alert on webhook processing latency > 5s, failure rate > 1%
* **Rate limiting**: Implement rate limiting on webhook endpoints (100 req/s per provider)
* **Circuit breaker**: Apply ADR-019 patterns to provider calls
