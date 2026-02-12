//go:build integration

// Package e2e provides end-to-end integration tests for the payment-order service.
// This file tests the Stripe Connect payment flow end-to-end, covering:
//   - Happy path: payment order → saga → Stripe webhook (succeeded) → ledger entries
//   - Failure path: payment decline → dunning escalation
//   - Webhook idempotency via Stripe event ID deduplication
//   - All using real CockroachDB and miniredis for persistence verification
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82/webhook"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway/stripe"
	paymenthttp "github.com/meridianhub/meridian/services/payment-order/adapters/http"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"google.golang.org/genproto/googleapis/type/money"
)

// ============================================================================
// Stripe E2E Test Infrastructure
// ============================================================================

const testWebhookSecret = "whsec_e2e_test_secret_key"

// stripeE2EEnv extends the base E2E environment with Stripe-specific components.
type stripeE2EEnv struct {
	*E2ETestEnvironment

	// Stripe webhook handler (the HTTP endpoint under test)
	StripeWebhookHandler *paymenthttp.StripeWebhookHandler

	// Redis for event processor idempotency and dunning
	Redis     *redis.Client
	MiniRedis *miniredis.Miniredis

	// Saga execution repository for verifying audit trail
	SagaExecRepo *persistence.SagaExecutionRepository

	Logger *slog.Logger
}

// setupStripeE2E creates a full Stripe E2E test environment.
func setupStripeE2E(t *testing.T, opts ...e2eOption) *stripeE2EEnv {
	t.Helper()

	// Start miniredis
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Ensure saga orchestration is enabled (prepend so caller opts can override).
	// setupE2E auto-wires a SagaExecutionRepository when saga orchestration is enabled.
	opts = append([]e2eOption{withSagaOrchestration()}, opts...)

	// Create base environment with saga orchestration enabled
	baseEnv := setupE2E(t, opts...)

	// Create saga execution repository for direct DB queries in tests
	sagaExecRepo := persistence.NewSagaExecutionRepository(baseEnv.DB)

	// Build the Stripe webhook handler chain:
	// StripeWebhookHandler → WebhookHandler → Service.UpdatePaymentOrder

	// The WebhookHandler wraps the real service for UpdatePaymentOrder calls
	webhookHandler, err := paymenthttp.NewWebhookHandler(paymenthttp.WebhookHandlerConfig{
		PaymentOrderService: baseEnv.Service,
		HMACSecret:          []byte("generic-hmac-secret"),
		Logger:              logger,
	})
	require.NoError(t, err)

	// Event processor for Stripe event-level idempotency + dunning
	eventProcessor, err := paymenthttp.NewStripeEventProcessor(paymenthttp.StripeEventProcessorConfig{
		RedisClient: redisClient,
		Logger:      logger,
	})
	require.NoError(t, err)

	// Tenant config provider returning test Stripe config.
	// Use the base environment's dynamic tenant ID so webhook requests
	// using that tenant context resolve the correct Stripe config.
	configProvider := &testTenantConfigProvider{
		configs: map[string]stripe.TenantConfig{
			string(baseEnv.TenantID): {
				ConnectedAccountID:    "acct_e2e_test_123",
				WebhookEndpointSecret: testWebhookSecret,
			},
		},
	}

	stripeCfg := stripe.DefaultConfig()
	stripeCfg.APIKey = "sk_test_e2e_key"

	factory, err := stripe.NewClientFactory(stripeCfg, configProvider, logger)
	require.NoError(t, err)

	stripeHandler, err := paymenthttp.NewStripeWebhookHandler(paymenthttp.StripeWebhookHandlerConfig{
		ClientFactory:  factory,
		WebhookHandler: webhookHandler,
		EventProcessor: eventProcessor,
		Logger:         logger,
	})
	require.NoError(t, err)

	return &stripeE2EEnv{
		E2ETestEnvironment:   baseEnv,
		StripeWebhookHandler: stripeHandler,
		Redis:                redisClient,
		MiniRedis:            mr,
		SagaExecRepo:         sagaExecRepo,
		Logger:               logger,
	}
}

// testTenantConfigProvider implements stripe.TenantConfigProvider for E2E tests.
type testTenantConfigProvider struct {
	configs map[string]stripe.TenantConfig
}

func (p *testTenantConfigProvider) GetTenantConfig(tenantID string) (stripe.TenantConfig, error) {
	cfg, ok := p.configs[tenantID]
	if !ok {
		return stripe.TenantConfig{}, stripe.ErrTenantConfigNotFound
	}
	return cfg, nil
}

// ============================================================================
// Webhook Helpers
// ============================================================================

// buildStripePayload constructs a Stripe webhook event payload.
func buildStripePayload(t *testing.T, eventID, eventType string, data map[string]any) []byte {
	t.Helper()
	payload := map[string]any{
		"id":      eventID,
		"type":    eventType,
		"created": time.Now().Unix(),
		"data":    map[string]any{"object": data},
	}
	out, err := json.Marshal(payload)
	require.NoError(t, err)
	return out
}

// signPayload signs a webhook payload with the test secret.
func signPayload(t *testing.T, payload []byte) string {
	t.Helper()
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  testWebhookSecret,
	})
	return signed.Header
}

// sendStripeWebhook sends a signed Stripe webhook request to the handler.
func sendStripeWebhook(t *testing.T, handler *paymenthttp.StripeWebhookHandler, tenantCtx context.Context, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	sig := signPayload(t, payload)
	req := httptest.NewRequest(http.MethodPost, "/webhook/stripe", bytes.NewReader(payload)).WithContext(tenantCtx)
	req.Header.Set("Stripe-Signature", sig)
	rr := httptest.NewRecorder()
	handler.HandleStripeWebhook(rr, req)
	return rr
}

// ============================================================================
// Test: Happy Path - Payment Order → Saga → Stripe Webhook → Ledger Entries
// ============================================================================

func TestStripeE2E_HappyPath_PaymentSucceeded(t *testing.T) {
	env := setupStripeE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx
	tenantCtx := ctx // Already contains tenant from base env setup

	// Step 1: Initiate payment order (triggers saga: reserve funds → send to gateway)
	req := createPaymentRequest("ACC-STRIPE-E2E-001", 250)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, initiateResp.PaymentOrder)
	assert.Equal(t, paymentorderv1.PaymentOrderStatus_PAYMENT_ORDER_STATUS_INITIATED,
		initiateResp.PaymentOrder.Status)

	paymentOrderID := initiateResp.PaymentOrder.PaymentOrderId
	poID, err := uuid.Parse(paymentOrderID)
	require.NoError(t, err)

	// Step 2: Wait for saga to reach EXECUTING (lien reserved + mock gateway accepted)
	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusExecuting, po.Status,
		"Payment should reach EXECUTING after saga completes")
	assert.NotEmpty(t, po.LienID, "Lien should be reserved")
	assert.NotEmpty(t, po.GatewayReferenceID, "Gateway reference should be set")

	// Step 3: Simulate Stripe payment_intent.succeeded webhook
	eventID := "evt_e2e_succeeded_" + uuid.New().String()[:8]
	payload := buildStripePayload(t, eventID, "payment_intent.succeeded", map[string]any{
		"id":       po.GatewayReferenceID, // Match the gateway ref from saga
		"object":   "payment_intent",
		"amount":   25000, // 250 GBP in pence
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": paymentOrderID},
		"status":   "succeeded",
	})

	rr := sendStripeWebhook(t, env.StripeWebhookHandler, tenantCtx, payload)
	assert.Equal(t, http.StatusOK, rr.Code, "Webhook should return 200 OK")

	// Step 4: Verify payment order reached COMPLETED
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			p, findErr := env.Repo.FindByID(ctx, poID)
			return findErr == nil && p.Status == domain.PaymentOrderStatusCompleted
		})
	require.NoError(t, err, "Payment order should reach COMPLETED after webhook")

	// Step 5: Verify ledger entries were posted
	assert.GreaterOrEqual(t, atomic.LoadInt32(&env.FinancialAccountingClient.initiateBookingLogCalls),
		int32(1), "Booking log should be initiated on SETTLED")
	assert.GreaterOrEqual(t, atomic.LoadInt32(&env.FinancialAccountingClient.captureLedgerPostingCalls),
		int32(2), "At least debit + credit ledger postings")

	// Step 6: Verify lien execution happened
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&env.CurrentAccountClient.executeLienCalls) >= 1
		})
	require.NoError(t, err, "ExecuteLien should be called after SETTLED")

	// Step 7: Verify Redis processed event key was set (idempotency)
	exists, err := env.Redis.Exists(ctx, "processed_webhook:"+eventID).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), exists, "Stripe event should be marked as processed in Redis")

	// Step 8: Verify dunning was NOT scheduled for successful payments
	dunningCount, err := env.Redis.ZCard(ctx, "dunning:retries").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), dunningCount, "No dunning for successful payments")

	// Step 9: Verify final DB state
	finalPO, err := env.Repo.FindByID(ctx, poID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusCompleted, finalPO.Status)
	assert.NotNil(t, finalPO.CompletedAt)
	assert.NotEmpty(t, finalPO.LedgerBookingID)
}

// ============================================================================
// Test: Failure Path - Payment Decline → Dunning Escalation
// ============================================================================

func TestStripeE2E_PaymentDeclined_DunningEscalation(t *testing.T) {
	env := setupStripeE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx
	tenantCtx := ctx // Already contains tenant from base env setup

	// Step 1: Initiate payment order
	req := createPaymentRequest("ACC-STRIPE-E2E-FAIL-001", 100)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	paymentOrderID := initiateResp.PaymentOrder.PaymentOrderId
	poID, err := uuid.Parse(paymentOrderID)
	require.NoError(t, err)

	// Step 2: Wait for saga to reach EXECUTING
	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// Step 3: Simulate Stripe payment_intent.payment_failed webhook
	eventID := "evt_e2e_failed_" + uuid.New().String()[:8]
	payload := buildStripePayload(t, eventID, "payment_intent.payment_failed", map[string]any{
		"id":       po.GatewayReferenceID,
		"object":   "payment_intent",
		"amount":   10000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": paymentOrderID},
		"status":   "requires_payment_method",
		"last_payment_error": map[string]any{
			"message": "Your card was declined",
			"code":    "card_declined",
		},
	})

	rr := sendStripeWebhook(t, env.StripeWebhookHandler, tenantCtx, payload)
	assert.Equal(t, http.StatusOK, rr.Code, "Webhook should return 200 OK even for failures")

	// Step 4: Verify payment order status → FAILED
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			p, findErr := env.Repo.FindByID(ctx, poID)
			return findErr == nil && p.Status == domain.PaymentOrderStatusFailed
		})
	require.NoError(t, err, "Payment order should reach FAILED after decline webhook")

	// Step 5: Verify dunning was scheduled in Redis ZSET
	members, err := env.Redis.ZRangeByScore(ctx, "dunning:retries", &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	require.NoError(t, err)
	assert.Len(t, members, 1, "Dunning should be scheduled for failed payment")
	assert.Equal(t, "stripe:"+paymentOrderID, members[0],
		"Dunning entry should reference the correct payment order")

	// Step 6: Verify the dunning score is a future timestamp (~24h from now)
	scores, err := env.Redis.ZRangeByScoreWithScores(ctx, "dunning:retries", &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	require.NoError(t, err)
	require.Len(t, scores, 1)
	dueAt := time.Unix(int64(scores[0].Score), 0)
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), dueAt, 5*time.Minute,
		"Dunning should be scheduled ~24h in the future")
}

// ============================================================================
// Test: Webhook Idempotency - Replay Same Event → No Duplicate Processing
// ============================================================================

func TestStripeE2E_WebhookIdempotency(t *testing.T) {
	env := setupStripeE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx
	tenantCtx := ctx // Already contains tenant from base env setup

	// Step 1: Create and advance payment to EXECUTING
	req := createPaymentRequest("ACC-STRIPE-E2E-IDEM-001", 500)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	paymentOrderID := initiateResp.PaymentOrder.PaymentOrderId
	poID, err := uuid.Parse(paymentOrderID)
	require.NoError(t, err)

	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// Step 2: Send first payment_intent.succeeded webhook
	eventID := "evt_e2e_idem_" + uuid.New().String()[:8]
	payload := buildStripePayload(t, eventID, "payment_intent.succeeded", map[string]any{
		"id":       po.GatewayReferenceID,
		"object":   "payment_intent",
		"amount":   50000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": paymentOrderID},
		"status":   "succeeded",
	})

	rr1 := sendStripeWebhook(t, env.StripeWebhookHandler, tenantCtx, payload)
	assert.Equal(t, http.StatusOK, rr1.Code, "First webhook should succeed")

	// Wait for COMPLETED
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			p, findErr := env.Repo.FindByID(ctx, poID)
			return findErr == nil && p.Status == domain.PaymentOrderStatusCompleted
		})
	require.NoError(t, err)

	// Wait for async lien execution to settle before recording baseline
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return atomic.LoadInt32(&env.CurrentAccountClient.executeLienCalls) >= 1
		})
	require.NoError(t, err)

	// Record call counts after first webhook has fully settled
	lienExecCallsAfterFirst := atomic.LoadInt32(&env.CurrentAccountClient.executeLienCalls)
	bookingLogCallsAfterFirst := atomic.LoadInt32(&env.FinancialAccountingClient.initiateBookingLogCalls)

	// Step 3: Replay the SAME event (same event ID, new signature)
	payload2 := buildStripePayload(t, eventID, "payment_intent.succeeded", map[string]any{
		"id":       po.GatewayReferenceID,
		"object":   "payment_intent",
		"amount":   50000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": paymentOrderID},
		"status":   "succeeded",
	})

	rr2 := sendStripeWebhook(t, env.StripeWebhookHandler, tenantCtx, payload2)
	assert.Equal(t, http.StatusOK, rr2.Code, "Replayed webhook should return 200 (idempotent)")

	// Step 4: Verify the response indicates already processed
	var resp paymenthttp.WebhookResponse
	err = json.Unmarshal(rr2.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Acknowledged, "Replayed event should be acknowledged")
	assert.Equal(t, "event already processed", resp.Message,
		"Replayed event should indicate already processed")

	// Step 5: Verify no additional service calls were made
	// Give a short window for any potential async processing
	_ = await.New().
		AtMost(500 * time.Millisecond).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return false // Always times out - we're waiting to ensure no extra calls
		})
	// Timeout is expected here

	assert.Equal(t, lienExecCallsAfterFirst, atomic.LoadInt32(&env.CurrentAccountClient.executeLienCalls),
		"No additional ExecuteLien calls for duplicate webhook")
	assert.Equal(t, bookingLogCallsAfterFirst, atomic.LoadInt32(&env.FinancialAccountingClient.initiateBookingLogCalls),
		"No additional booking log calls for duplicate webhook")

	// Step 6: Payment should still be COMPLETED
	finalPO, err := env.Repo.FindByID(ctx, poID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentOrderStatusCompleted, finalPO.Status)
}

// ============================================================================
// Test: Saga Execution Audit Trail
// ============================================================================

func TestStripeE2E_SagaExecutionPersisted(t *testing.T) {
	env := setupStripeE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx

	// Create and advance payment to EXECUTING
	req := createPaymentRequest("ACC-STRIPE-E2E-SAGA-001", 300)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	poID, err := uuid.Parse(initiateResp.PaymentOrder.PaymentOrderId)
	require.NoError(t, err)

	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	assert.Equal(t, domain.PaymentOrderStatusExecuting, po.Status,
		"Payment should reach EXECUTING after saga")

	// Verify saga side-effects via service state
	assert.NotEmpty(t, po.LienID, "Saga should have reserved funds (lien)")
	assert.NotEmpty(t, po.GatewayReferenceID, "Saga should have sent to gateway")
	assert.Equal(t, int32(1), atomic.LoadInt32(&env.CurrentAccountClient.initiateLienCalls),
		"Saga should call InitiateLien once")
	assert.Equal(t, int32(1), atomic.LoadInt32(&env.PaymentGateway.sendPaymentCalls),
		"Saga should call SendPayment once")

	// Verify saga execution was persisted to the saga_executions table in CockroachDB.
	// The orchestrator logs RUNNING → COMPLETED records via SagaExecutionLogger,
	// which is wired to a SagaExecutionRepository backed by the real DB.
	sqlDB, dbErr := env.DB.DB()
	require.NoError(t, dbErr)

	type sagaRow struct {
		SagaName       string
		Status         string
		PaymentOrderID string
	}

	var rows []sagaRow
	queryErr := await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		UntilNoError(func() error {
			rows = nil
			sqlRows, err := sqlDB.QueryContext(ctx,
				"SELECT saga_name, status, payment_order_id::TEXT FROM saga_executions WHERE payment_order_id = $1 ORDER BY started_at",
				poID,
			)
			if err != nil {
				return err
			}
			defer sqlRows.Close()
			for sqlRows.Next() {
				var r sagaRow
				if err := sqlRows.Scan(&r.SagaName, &r.Status, &r.PaymentOrderID); err != nil {
					return err
				}
				rows = append(rows, r)
			}
			if len(rows) < 2 {
				return fmt.Errorf("expected at least 2 saga_execution rows (RUNNING + COMPLETED), got %d", len(rows))
			}
			return sqlRows.Err()
		})
	require.NoError(t, queryErr, "saga_executions rows should be persisted to CockroachDB")

	// First record should be RUNNING
	assert.Equal(t, "payment_execution", rows[0].SagaName)
	assert.Equal(t, "RUNNING", rows[0].Status)
	assert.Equal(t, poID.String(), rows[0].PaymentOrderID)

	// Last record should be COMPLETED
	last := rows[len(rows)-1]
	assert.Equal(t, "payment_execution", last.SagaName)
	assert.Equal(t, "COMPLETED", last.Status)
	assert.Equal(t, poID.String(), last.PaymentOrderID)
}

// ============================================================================
// Test: Multiple Webhooks for Different Events on Same Payment
// ============================================================================

func TestStripeE2E_MultipleDistinctWebhooks(t *testing.T) {
	env := setupStripeE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx
	tenantCtx := ctx // Already contains tenant from base env setup

	// Create and advance payment to EXECUTING
	req := createPaymentRequest("ACC-STRIPE-E2E-MULTI-001", 750)
	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	paymentOrderID := initiateResp.PaymentOrder.PaymentOrderId
	poID, err := uuid.Parse(paymentOrderID)
	require.NoError(t, err)

	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// Send payment_intent.succeeded webhook
	succeededEventID := "evt_multi_succ_" + uuid.New().String()[:8]
	succeededPayload := buildStripePayload(t, succeededEventID, "payment_intent.succeeded", map[string]any{
		"id":       po.GatewayReferenceID,
		"object":   "payment_intent",
		"amount":   75000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": paymentOrderID},
		"status":   "succeeded",
	})

	rr := sendStripeWebhook(t, env.StripeWebhookHandler, tenantCtx, succeededPayload)
	assert.Equal(t, http.StatusOK, rr.Code)

	// Wait for COMPLETED
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			p, findErr := env.Repo.FindByID(ctx, poID)
			return findErr == nil && p.Status == domain.PaymentOrderStatusCompleted
		})
	require.NoError(t, err)

	// Now send a DIFFERENT event type (payment_intent.payment_failed) for the same payment.
	// This has a different event ID, so it should NOT be deduplicated by event processor.
	// However, the service should handle this gracefully since the order is already COMPLETED.
	failedEventID := "evt_multi_fail_" + uuid.New().String()[:8]
	failedPayload := buildStripePayload(t, failedEventID, "payment_intent.payment_failed", map[string]any{
		"id":       po.GatewayReferenceID,
		"object":   "payment_intent",
		"amount":   75000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": paymentOrderID},
		"status":   "requires_payment_method",
		"last_payment_error": map[string]any{
			"message": "late failure notification",
		},
	})

	rr2 := sendStripeWebhook(t, env.StripeWebhookHandler, tenantCtx, failedPayload)
	// The handler should return 200 or an error depending on whether the service
	// allows status transitions from COMPLETED. Either way, verifying it doesn't panic.
	assert.Contains(t, []int{http.StatusOK, http.StatusInternalServerError}, rr2.Code,
		"Handler should respond without panicking for conflicting webhook")

	// Both events should be tracked in Redis
	exists1, _ := env.Redis.Exists(ctx, "processed_webhook:"+succeededEventID).Result()
	exists2, _ := env.Redis.Exists(ctx, "processed_webhook:"+failedEventID).Result()
	assert.Equal(t, int64(1), exists1, "Succeeded event should be tracked")
	assert.Equal(t, int64(1), exists2, "Failed event should be tracked (even if it didn't change state)")
}

// ============================================================================
// Test: Successful Payment Does Not Trigger Dunning
// ============================================================================

func TestStripeE2E_SucceededPayment_NoDunning(t *testing.T) {
	env := setupStripeE2E(t)
	defer env.Cleanup()

	ctx := env.Ctx
	tenantCtx := ctx // Already contains tenant from base env setup

	// Create and advance payment
	req := &paymentorderv1.InitiatePaymentOrderRequest{
		DebtorAccountId:   "ACC-STRIPE-E2E-NODUN-001",
		CreditorReference: "GB82WEST12345698765432",
		Amount: &commonv1.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        150,
				Nanos:        0,
			},
		},
		IdempotencyKey: &commonv1.IdempotencyKey{Key: uuid.New().String()},
	}

	initiateResp, err := env.Service.InitiatePaymentOrder(ctx, req)
	require.NoError(t, err)

	paymentOrderID := initiateResp.PaymentOrder.PaymentOrderId
	poID, err := uuid.Parse(paymentOrderID)
	require.NoError(t, err)

	po := waitForSagaTerminal(ctx, t, env.Repo, poID)
	require.Equal(t, domain.PaymentOrderStatusExecuting, po.Status)

	// Send payment_intent.succeeded
	eventID := "evt_nodun_" + uuid.New().String()[:8]
	payload := buildStripePayload(t, eventID, "payment_intent.succeeded", map[string]any{
		"id":       po.GatewayReferenceID,
		"object":   "payment_intent",
		"amount":   15000,
		"currency": "gbp",
		"metadata": map[string]string{"payment_order_id": paymentOrderID},
		"status":   "succeeded",
	})

	rr := sendStripeWebhook(t, env.StripeWebhookHandler, tenantCtx, payload)
	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify dunning ZSET is empty (succeeded payments should never trigger dunning)
	dunningCount, err := env.Redis.ZCard(ctx, "dunning:retries").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), dunningCount, "No dunning for successful payments")
}
