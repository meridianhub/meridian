package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	partyhttp "github.com/meridianhub/meridian/services/party/adapters/http"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/service"
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

const integrationTestTenantID = "test_tenant"

// testEventPublisher captures published events for assertions.
type testEventPublisher struct {
	events []service.VerificationCompletedEvent
}

func (p *testEventPublisher) PublishVerificationCompleted(_ context.Context, event service.VerificationCompletedEvent) error {
	p.events = append(p.events, event)
	return nil
}

// setupIntegrationTest creates a full integration test environment with:
// - CockroachDB/Postgres testcontainer
// - Tenant schema with party and party_verification tables
// - Real repositories (PartyRepository, VerificationRepository)
// - Real VerificationService wired with MockProvider
// - Webhook handler with HMAC secret
func setupIntegrationTest(t *testing.T) (*integrationTestEnv, func()) {
	t.Helper()

	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.PartyEntity{},
		&persistence.PartyVerificationEntity{},
		&audit.AuditOutbox{},
	})

	// Create the tenant schema
	tid := tenant.TenantID(integrationTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create party table in tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party (
		id UUID PRIMARY KEY,
		party_type VARCHAR(20) NOT NULL,
		legal_name VARCHAR(255) NOT NULL,
		display_name VARCHAR(255),
		status VARCHAR(20) NOT NULL,
		external_reference VARCHAR(255),
		external_reference_type VARCHAR(50),
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		UNIQUE(external_reference, external_reference_type)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create party_verification table in tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_verification (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL REFERENCES %s.party(id) ON DELETE CASCADE,
		verification_id VARCHAR(255) NOT NULL UNIQUE,
		provider VARCHAR(100) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
		risk_score DECIMAL(5,4),
		reason TEXT,
		completed_at TIMESTAMP WITH TIME ZONE,
		metadata JSONB DEFAULT '{}',
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
	)`, schemaName, schemaName)).Error
	require.NoError(t, err)

	// Create audit_outbox table in tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL,
		record_id VARCHAR(50) NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)

	// Create repositories
	partyRepo := persistence.NewRepository(db)
	verificationRepo := persistence.NewVerificationRepository(db)

	// Create mock provider
	provider := verification.NewMockProvider().
		WithAlwaysApprove(true).
		WithAsyncMode(true)

	// Create event publisher
	eventPublisher := &testEventPublisher{}

	// Create verification service
	svc, err := service.NewVerificationService(
		partyRepo,
		verificationRepo,
		provider,
		eventPublisher,
		nil, // logger
	)
	require.NoError(t, err)

	// Create webhook handler
	hmacSecret := []byte("integration-test-secret")
	webhookHandler, err := partyhttp.NewVerificationWebhookHandler(partyhttp.VerificationWebhookHandlerConfig{
		VerificationService: svc,
		HMACSecrets:         map[string][]byte{"default": hmacSecret, "onfido": hmacSecret},
	})
	require.NoError(t, err)

	env := &integrationTestEnv{
		db:               db,
		ctx:              ctx,
		schemaName:       schemaName,
		partyRepo:        partyRepo,
		verificationRepo: verificationRepo,
		provider:         provider,
		eventPublisher:   eventPublisher,
		svc:              svc,
		webhookHandler:   webhookHandler,
		hmacSecret:       hmacSecret,
	}

	return env, dbCleanup
}

type integrationTestEnv struct {
	db               *gorm.DB
	ctx              context.Context
	schemaName       string
	partyRepo        *persistence.Repository
	verificationRepo *persistence.VerificationRepository
	provider         *verification.MockProvider
	eventPublisher   *testEventPublisher
	svc              *service.VerificationService
	webhookHandler   *partyhttp.VerificationWebhookHandler
	hmacSecret       []byte
}

// createParty inserts a test party directly into the database and returns its ID.
// Uses schema-qualified table name to avoid reliance on session-scoped search_path.
func (e *integrationTestEnv) createParty(t *testing.T) uuid.UUID {
	t.Helper()
	partyID := uuid.New()
	now := time.Now()

	err := e.db.Exec(fmt.Sprintf(`
		INSERT INTO %s.party (id, party_type, legal_name, status, version, created_at, updated_at, created_by, updated_by)
		VALUES (?, 'PERSON', 'Integration Test Person', 'ACTIVE', 1, ?, ?, 'system', 'system')
	`, pq.QuoteIdentifier(e.schemaName)), partyID, now, now).Error
	require.NoError(t, err)

	return partyID
}

// sendWebhook sends a signed webhook request to the handler and returns the response.
// The request context is injected with the test tenant to simulate middleware behavior.
func (e *integrationTestEnv) sendWebhook(t *testing.T, webhookReq partyhttp.VerificationWebhookRequest, provider string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := partyhttp.GenerateWebhookSignature(body, e.hmacSecret)

	path := fmt.Sprintf("/webhooks/verification/%s", provider)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(partyhttp.WebhookSignatureHeader, signature)

	// Inject tenant context to simulate middleware that would normally set this
	req = req.WithContext(e.ctx)

	rr := httptest.NewRecorder()
	e.webhookHandler.HandleWebhook(rr, req)
	return rr
}

// TestIntegration_FullAsyncWorkflow exercises the complete async verification flow:
// InitiateVerification -> PENDING status -> Webhook callback -> APPROVED status.
func TestIntegration_FullAsyncWorkflow(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// 1. Create a party
	partyID := env.createParty(t)

	// 2. Initiate verification
	resp, err := env.svc.InitiateVerification(env.ctx, service.InitiateVerificationRequest{
		PartyID:  partyID,
		Provider: "onfido",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "PENDING", resp.Status)
	assert.NotEqual(t, uuid.Nil, resp.VerificationID)
	assert.NotEmpty(t, resp.ProviderVerificationID)

	// 3. Verify DB record is PENDING
	entity, err := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, "PENDING", entity.Status)
	assert.Equal(t, partyID, entity.PartyID)

	// 4. Simulate webhook callback with APPROVED status
	riskScore := 0.15
	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: resp.ProviderVerificationID,
		Status:         "APPROVED",
		RiskScore:      &riskScore,
		Reason:         "Identity verified successfully",
		Timestamp:      time.Now().UTC(),
		Metadata:       map[string]string{"check_id": "chk-int-123"},
	}

	rr := env.sendWebhook(t, webhookReq, "onfido")
	assert.Equal(t, http.StatusOK, rr.Code)

	var webhookResp partyhttp.VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&webhookResp)
	require.NoError(t, err)
	assert.True(t, webhookResp.Acknowledged)

	// 5. Poll DB until status updates to APPROVED
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			updated, lookupErr := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
			if lookupErr != nil {
				return false
			}
			return updated.Status == "APPROVED"
		})
	require.NoError(t, err, "verification should transition to APPROVED")

	// 6. Verify final state in DB
	final, err := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", final.Status)
	assert.NotNil(t, final.RiskScore)
	assert.InDelta(t, 0.15, *final.RiskScore, 0.0001)
	assert.NotNil(t, final.Reason)
	assert.Equal(t, "Identity verified successfully", *final.Reason)
	assert.NotNil(t, final.CompletedAt)

	// 7. Verify event was published
	require.Len(t, env.eventPublisher.events, 1)
	event := env.eventPublisher.events[0]
	assert.Equal(t, partyID.String(), event.PartyID)
	assert.Equal(t, "APPROVED", event.Status)
	assert.InDelta(t, 0.15, *event.RiskScore, 0.0001)
}

// TestIntegration_FullAsyncWorkflow_Rejected tests the rejection path.
func TestIntegration_FullAsyncWorkflow_Rejected(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	partyID := env.createParty(t)

	// Initiate verification
	resp, err := env.svc.InitiateVerification(env.ctx, service.InitiateVerificationRequest{
		PartyID:  partyID,
		Provider: "onfido",
	})
	require.NoError(t, err)
	assert.Equal(t, "PENDING", resp.Status)

	// Webhook callback with REJECTED status
	riskScore := 0.92
	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: resp.ProviderVerificationID,
		Status:         "REJECTED",
		RiskScore:      &riskScore,
		Reason:         "Document fraud detected",
		Timestamp:      time.Now().UTC(),
	}

	rr := env.sendWebhook(t, webhookReq, "onfido")
	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify final state
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			updated, lookupErr := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
			if lookupErr != nil {
				return false
			}
			return updated.Status == "REJECTED"
		})
	require.NoError(t, err, "verification should transition to REJECTED")

	final, err := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, "REJECTED", final.Status)
	assert.NotNil(t, final.RiskScore)
	assert.InDelta(t, 0.92, *final.RiskScore, 0.0001)

	// Event should be published
	require.Len(t, env.eventPublisher.events, 1)
	assert.Equal(t, "REJECTED", env.eventPublisher.events[0].Status)
}

// TestIntegration_WebhookValidSignature verifies that a properly signed webhook
// updates the verification status in the database.
func TestIntegration_WebhookValidSignature(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	partyID := env.createParty(t)

	// Create a PENDING verification
	resp, err := env.svc.InitiateVerification(env.ctx, service.InitiateVerificationRequest{
		PartyID:  partyID,
		Provider: "onfido",
	})
	require.NoError(t, err)

	// Send valid webhook
	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: resp.ProviderVerificationID,
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	rr := env.sendWebhook(t, webhookReq, "default")
	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify status updated
	updated, err := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", updated.Status)
}

// TestIntegration_WebhookInvalidSignature verifies that a webhook with an
// incorrect HMAC signature is rejected with 401.
func TestIntegration_WebhookInvalidSignature(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: "any-id",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	// Sign with wrong secret
	wrongSignature := partyhttp.GenerateWebhookSignature(body, []byte("wrong-secret"))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(partyhttp.WebhookSignatureHeader, wrongSignature)

	rr := httptest.NewRecorder()
	env.webhookHandler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var resp partyhttp.VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
}

// TestIntegration_WebhookReplayAttack verifies that a webhook with an expired
// timestamp is rejected with 400.
func TestIntegration_WebhookReplayAttack(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Create a webhook with a timestamp 10 minutes ago (beyond DefaultWebhookMaxAge of 5min)
	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: "replay-verify-123",
		Status:         "APPROVED",
		Timestamp:      time.Now().Add(-10 * time.Minute),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := partyhttp.GenerateWebhookSignature(body, env.hmacSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/verification/default", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(partyhttp.WebhookSignatureHeader, signature)

	rr := httptest.NewRecorder()
	env.webhookHandler.HandleWebhook(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp partyhttp.VerificationWebhookResponse
	err = json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
	assert.Contains(t, resp.Error, "expired")
}

// TestIntegration_WebhookIdempotency verifies that duplicate webhook delivery
// returns 200 without error (idempotent handling).
func TestIntegration_WebhookIdempotency(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	partyID := env.createParty(t)

	// Initiate verification
	resp, err := env.svc.InitiateVerification(env.ctx, service.InitiateVerificationRequest{
		PartyID:  partyID,
		Provider: "onfido",
	})
	require.NoError(t, err)

	// First webhook delivery - should succeed
	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: resp.ProviderVerificationID,
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	rr1 := env.sendWebhook(t, webhookReq, "default")
	assert.Equal(t, http.StatusOK, rr1.Code)

	// Verify status is now APPROVED
	updated, err := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", updated.Status)

	// Second (duplicate) webhook delivery - should return 200 (idempotent)
	webhookReq2 := partyhttp.VerificationWebhookRequest{
		VerificationID: resp.ProviderVerificationID,
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	rr2 := env.sendWebhook(t, webhookReq2, "default")
	assert.Equal(t, http.StatusOK, rr2.Code)

	var resp2 partyhttp.VerificationWebhookResponse
	err = json.NewDecoder(rr2.Body).Decode(&resp2)
	require.NoError(t, err)
	assert.True(t, resp2.Acknowledged)
	assert.Contains(t, resp2.Message, "already processed")

	// Only one event should have been published (from the first webhook)
	assert.Len(t, env.eventPublisher.events, 1)
}

// TestIntegration_WebhookNonExistentVerification verifies that a webhook for an
// unknown verification ID returns 404.
func TestIntegration_WebhookNonExistentVerification(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: "nonexistent-verification-id",
		Status:         "APPROVED",
		Timestamp:      time.Now().UTC(),
	}
	rr := env.sendWebhook(t, webhookReq, "default")

	assert.Equal(t, http.StatusNotFound, rr.Code)

	var resp partyhttp.VerificationWebhookResponse
	err := json.NewDecoder(rr.Body).Decode(&resp)
	require.NoError(t, err)
	assert.False(t, resp.Acknowledged)
}

// TestIntegration_InitiateVerification_PartyNotFound verifies that initiating
// verification for a non-existent party returns an appropriate error.
func TestIntegration_InitiateVerification_PartyNotFound(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	_, err := env.svc.InitiateVerification(env.ctx, service.InitiateVerificationRequest{
		PartyID:  uuid.New(), // Non-existent party
		Provider: "onfido",
	})
	assert.ErrorIs(t, err, service.ErrPartyNotFoundForVerification)
}

// TestIntegration_ListVerificationsForParty verifies that all verifications
// for a party can be retrieved after multiple initiations.
func TestIntegration_ListVerificationsForParty(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	partyID := env.createParty(t)

	// Initiate multiple verifications
	for i := 0; i < 3; i++ {
		_, err := env.svc.InitiateVerification(env.ctx, service.InitiateVerificationRequest{
			PartyID:  partyID,
			Provider: "onfido",
		})
		require.NoError(t, err)
	}

	// List all verifications for this party
	verifications, err := env.svc.ListVerificationsForParty(env.ctx, partyID)
	require.NoError(t, err)
	assert.Len(t, verifications, 3)

	for _, v := range verifications {
		assert.Equal(t, partyID, v.PartyID)
		assert.Equal(t, "PENDING", v.Status)
	}
}

// TestIntegration_WebhookWithMetadata verifies that webhook metadata is persisted
// through the full flow.
func TestIntegration_WebhookWithMetadata(t *testing.T) {
	env, cleanup := setupIntegrationTest(t)
	defer cleanup()

	partyID := env.createParty(t)

	resp, err := env.svc.InitiateVerification(env.ctx, service.InitiateVerificationRequest{
		PartyID:  partyID,
		Provider: "onfido",
	})
	require.NoError(t, err)

	// Send webhook with metadata
	riskScore := 0.05
	webhookReq := partyhttp.VerificationWebhookRequest{
		VerificationID: resp.ProviderVerificationID,
		Status:         "APPROVED",
		RiskScore:      &riskScore,
		Reason:         "All checks passed",
		Timestamp:      time.Now().UTC(),
		Metadata: map[string]string{
			"document_type": "passport",
			"country":       "GB",
			"confidence":    "0.99",
		},
	}
	rr := env.sendWebhook(t, webhookReq, "default")
	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify metadata persisted
	final, err := env.verificationRepo.GetVerificationByID(env.ctx, resp.VerificationID)
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", final.Status)
	require.NotNil(t, final.Metadata)

	var metadata map[string]string
	err = json.Unmarshal([]byte(*final.Metadata), &metadata)
	require.NoError(t, err)
	assert.Equal(t, "passport", metadata["document_type"])
	assert.Equal(t, "GB", metadata["country"])

	// Verify event metadata
	require.Len(t, env.eventPublisher.events, 1)
	assert.Equal(t, "passport", env.eventPublisher.events[0].Metadata["document_type"])
}
