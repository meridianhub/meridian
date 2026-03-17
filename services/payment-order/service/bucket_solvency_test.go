package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sentinel errors for testing
var errMockInstrumentNotFound = errors.New("instrument not found")

// MockReferenceDataClient implements ReferenceDataClient for testing
type MockReferenceDataClient struct {
	instruments map[string]*InstrumentInfo
	err         error
	sagaScript  string // Custom saga script (if empty, uses default)
	getSagaErr  error  // Custom GetSaga error
}

func NewMockReferenceDataClient() *MockReferenceDataClient {
	return &MockReferenceDataClient{
		instruments: make(map[string]*InstrumentInfo),
	}
}

func (m *MockReferenceDataClient) RetrieveInstrument(_ context.Context, code string) (*InstrumentInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	info, ok := m.instruments[code]
	if !ok {
		return nil, errMockInstrumentNotFound
	}
	return info, nil
}

func (m *MockReferenceDataClient) GetSaga(_ context.Context, name string, version int) (*SagaDefinition, error) {
	if m.getSagaErr != nil {
		return nil, m.getSagaErr
	}

	script := m.sagaScript
	if script == "" {
		// Default payment_execution saga script using typed service modules
		script = `# Saga: payment_execution
# Version: 1.0.0

def payment_execution():
    ctx = input_data

    # Step 1: Reserve funds with bucket-aware lien
    step(name="reserve_funds")
    lien_result = payment_order.create_lien(
        account_id=ctx.get("debtor_account_id"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        payment_order_id=ctx.get("payment_order_id"),
        instrument_code=ctx.get("instrument_code", ""),
        payment_attributes=ctx.get("payment_attributes", {}),
    )

    lien_id = lien_result.lien_id
    bucket_id = lien_result.bucket_id

    # Step 2: Send payment to gateway
    step(name="send_to_gateway")
    gateway_result = payment_order.send_to_gateway(
        payment_order_id=ctx.get("payment_order_id"),
        debtor_account_id=ctx.get("debtor_account_id"),
        creditor_reference=ctx.get("creditor_reference"),
        amount_cents=ctx.get("amount_cents"),
        currency=ctx.get("currency"),
        idempotency_key=ctx.get("idempotency_key"),
    )

    gateway_reference_id = gateway_result.gateway_reference_id
    gateway_status = gateway_result.gateway_status

    result = {
        "lien_id": lien_id,
        "bucket_id": bucket_id,
        "gateway_reference_id": gateway_reference_id,
        "gateway_status": gateway_status,
    }

    if ctx.get("should_post_ledger", False):
        step(name="post_ledger_entries")
        ledger_result = payment_order.post_ledger_entries(
            payment_order_id=ctx.get("payment_order_id"),
            debtor_account_id=ctx.get("debtor_account_id"),
            gateway_reference_id=gateway_reference_id,
            amount_cents=ctx.get("amount_cents"),
            currency=ctx.get("currency"),
            idempotency_key=ctx.get("idempotency_key"),
            internal_clearing_enabled=ctx.get("internal_clearing_enabled", False),
        )
        result["booking_log_id"] = ledger_result.booking_log_id

    if ctx.get("should_execute_lien", False):
        if lien_id:
            step(name="execute_lien")
            execution_result = payment_order.execute_lien(
                lien_id=lien_id,
            )
            result["lien_execution_status"] = execution_result.execution_status

    return result

output = payment_execution()
`
	}

	return &SagaDefinition{
		ID:      uuid.New().String(),
		Name:    name,
		Version: version,
		Script:  script,
		Status:  "ACTIVE",
	}, nil
}

func (m *MockReferenceDataClient) Close() error {
	return nil
}

func (m *MockReferenceDataClient) AddInstrument(code string, info *InstrumentInfo) {
	m.instruments[code] = info
}

func TestPaymentOrchestrator_EvaluateBucketID_NoInstrumentCode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:              logger,
		Repo:                repo,
		ReferenceDataClient: NewMockReferenceDataClient(),
	})
	require.NoError(t, err)

	po := &domain.PaymentOrder{
		ID:             uuid.New(),
		InstrumentCode: "", // No instrument code
	}

	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err)
	assert.Equal(t, "", bucketID, "should return empty bucket ID when no instrument code")
}

func TestPaymentOrchestrator_EvaluateBucketID_NoReferenceDataClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:              logger,
		Repo:                repo,
		ReferenceDataClient: nil, // No client configured
	})
	require.NoError(t, err)

	po := &domain.PaymentOrder{
		ID:             uuid.New(),
		InstrumentCode: "RICE_V1",
	}

	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err)
	assert.Equal(t, "", bucketID, "should return empty bucket ID when no reference data client")
}

func TestPaymentOrchestrator_EvaluateBucketID_InstrumentNotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()
	refClient := NewMockReferenceDataClient()
	// Don't add the instrument - it won't be found

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:              logger,
		Repo:                repo,
		ReferenceDataClient: refClient,
	})
	require.NoError(t, err)

	po := &domain.PaymentOrder{
		ID:             uuid.New(),
		InstrumentCode: "UNKNOWN",
	}

	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err) // Should not error, just use default bucket
	assert.Equal(t, "", bucketID)
}

func TestPaymentOrchestrator_EvaluateBucketID_NoFungibilityExpression(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()
	refClient := NewMockReferenceDataClient()
	refClient.AddInstrument("USD", &InstrumentInfo{
		Code:                     "USD",
		Version:                  1,
		FungibilityKeyExpression: "", // No expression = fully fungible
	})

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:              logger,
		Repo:                repo,
		ReferenceDataClient: refClient,
	})
	require.NoError(t, err)

	po := &domain.PaymentOrder{
		ID:             uuid.New(),
		InstrumentCode: "USD",
	}

	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err)
	assert.Equal(t, "", bucketID, "fully fungible instrument should have empty bucket ID")
}

func TestPaymentOrchestrator_EvaluateBucketID_WithFungibilityExpression(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()
	refClient := NewMockReferenceDataClient()
	refClient.AddInstrument("RICE_V1", &InstrumentInfo{
		Code:                     "RICE_V1",
		Version:                  1,
		FungibilityKeyExpression: `instrument_code + ":" + attributes.grade`,
	})

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:              logger,
		Repo:                repo,
		ReferenceDataClient: refClient,
	})
	require.NoError(t, err)

	po := &domain.PaymentOrder{
		ID:                uuid.New(),
		InstrumentCode:    "RICE_V1",
		PaymentAttributes: map[string]string{"grade": "A"},
	}

	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err)
	assert.Equal(t, "RICE_V1:A", bucketID)
}

func TestPaymentOrchestrator_EvaluateBucketID_ComplexExpression(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()
	refClient := NewMockReferenceDataClient()
	refClient.AddInstrument("COFFEE_FUTURES", &InstrumentInfo{
		Code:                     "COFFEE_FUTURES",
		Version:                  2,
		FungibilityKeyExpression: `instrument_code + ":" + attributes.origin + "-" + attributes.grade`,
	})

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:              logger,
		Repo:                repo,
		ReferenceDataClient: refClient,
	})
	require.NoError(t, err)

	po := &domain.PaymentOrder{
		ID:             uuid.New(),
		InstrumentCode: "COFFEE_FUTURES",
		PaymentAttributes: map[string]string{
			"origin": "BR",
			"grade":  "AA",
		},
	}

	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err)
	assert.Equal(t, "COFFEE_FUTURES:BR-AA", bucketID)
}

func TestPaymentOrchestrator_Orchestrate_PassesBucketIDToLien(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()

	// Setup reference data client with instrument
	refClient := NewMockReferenceDataClient()
	refClient.AddInstrument("RICE_V1", &InstrumentInfo{
		Code:                     "RICE_V1",
		Version:                  1,
		FungibilityKeyExpression: `instrument_code + ":" + attributes.grade`,
	})

	// Setup current account client
	mockCA := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-123",
			},
		},
	}

	// Setup gateway
	mockGateway := &MockPaymentGateway{
		response: gateway.PaymentResponse{
			Status:             gateway.StatusAccepted,
			GatewayReferenceID: "gw-ref-123",
		},
	}

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGateway,
		ReferenceDataClient:  refClient,
		LienExecutionRetryConfig: &sharedclients.RetryConfig{
			MaxRetries:      1,
			InitialInterval: 1 * time.Millisecond,
		},
		SagaOrchestrationEnabled: true,
	})
	require.NoError(t, err)

	// Create payment order with instrument code and attributes
	money, _ := domain.NewMoney("GBP", 10000)
	po := &domain.PaymentOrder{
		ID:                uuid.New(),
		DebtorAccountID:   "account-123",
		CreditorReference: "creditor-456",
		Amount:            money,
		Status:            domain.PaymentOrderStatusInitiated,
		InstrumentCode:    "RICE_V1",
		PaymentAttributes: map[string]string{"grade": "A"},
		CorrelationID:     uuid.New().String(),
		IdempotencyKey:    uuid.New().String(),
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		Version:           1,
	}

	// Save to repository
	err = repo.Create(context.Background(), po)
	require.NoError(t, err)

	// Execute orchestration (runs asynchronously)
	orchestrator.Orchestrate(context.Background(), po)

	// Wait for lien to be initiated
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockCA.initiateLienCalled
		})
	require.NoError(t, err, "InitiateLien should have been called")

	// Verify bucket_id was passed to InitiateLien
	require.NotNil(t, mockCA.lastInitiateLienRequest)
	assert.Equal(t, "RICE_V1:A", mockCA.lastInitiateLienRequest.BucketId,
		"bucket_id should be passed to InitiateLien request")
	assert.Equal(t, "account-123", mockCA.lastInitiateLienRequest.AccountId)
	assert.Equal(t, po.ID.String(), mockCA.lastInitiateLienRequest.PaymentOrderReference)
}

func TestPaymentOrchestrator_Orchestrate_NoBucketIDForFullyFungible(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()

	// Setup reference data client with fully fungible instrument
	refClient := NewMockReferenceDataClient()
	refClient.AddInstrument("USD", &InstrumentInfo{
		Code:                     "USD",
		Version:                  1,
		FungibilityKeyExpression: "", // No expression = fully fungible
	})

	// Setup current account client
	mockCA := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-456",
			},
		},
	}

	// Setup gateway
	mockGateway := &MockPaymentGateway{
		response: gateway.PaymentResponse{
			Status:             gateway.StatusAccepted,
			GatewayReferenceID: "gw-ref-456",
		},
	}

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGateway,
		ReferenceDataClient:  refClient,
		LienExecutionRetryConfig: &sharedclients.RetryConfig{
			MaxRetries:      1,
			InitialInterval: 1 * time.Millisecond,
		},
		SagaOrchestrationEnabled: true,
	})
	require.NoError(t, err)

	// Create payment order with instrument code but no special attributes
	money, _ := domain.NewMoney("USD", 5000)
	po := &domain.PaymentOrder{
		ID:                uuid.New(),
		DebtorAccountID:   "account-789",
		CreditorReference: "creditor-xyz",
		Amount:            money,
		Status:            domain.PaymentOrderStatusInitiated,
		InstrumentCode:    "USD",
		PaymentAttributes: nil,
		CorrelationID:     uuid.New().String(),
		IdempotencyKey:    uuid.New().String(),
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		Version:           1,
	}

	// Save to repository
	err = repo.Create(context.Background(), po)
	require.NoError(t, err)

	// Execute orchestration
	orchestrator.Orchestrate(context.Background(), po)

	// Wait for lien to be initiated
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return mockCA.initiateLienCalled
		})
	require.NoError(t, err, "InitiateLien should have been called")

	// Verify no bucket_id was passed for fully fungible instrument
	require.NotNil(t, mockCA.lastInitiateLienRequest)
	assert.Equal(t, "", mockCA.lastInitiateLienRequest.BucketId,
		"bucket_id should be empty for fully fungible instrument")
}

func TestPaymentOrchestrator_Orchestrate_UpdatesPaymentOrderWithBucketID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()

	// Setup reference data client with instrument
	refClient := NewMockReferenceDataClient()
	refClient.AddInstrument("RICE_V1", &InstrumentInfo{
		Code:                     "RICE_V1",
		Version:                  1,
		FungibilityKeyExpression: `instrument_code + ":" + attributes.grade`,
	})

	// Setup current account client
	mockCA := &MockCurrentAccountClient{
		initiateLienResp: &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{
				LienId: "lien-789",
			},
		},
	}

	// Setup gateway
	mockGateway := &MockPaymentGateway{
		response: gateway.PaymentResponse{
			Status:             gateway.StatusAccepted,
			GatewayReferenceID: "gw-ref-789",
		},
	}

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		CurrentAccountClient: mockCA,
		PaymentGateway:       mockGateway,
		ReferenceDataClient:  refClient,
		LienExecutionRetryConfig: &sharedclients.RetryConfig{
			MaxRetries:      1,
			InitialInterval: 1 * time.Millisecond,
		},
		SagaOrchestrationEnabled: true,
	})
	require.NoError(t, err)

	// Create payment order
	money, _ := domain.NewMoney("GBP", 10000)
	po := &domain.PaymentOrder{
		ID:                uuid.New(),
		DebtorAccountID:   "account-aaa",
		CreditorReference: "creditor-bbb",
		Amount:            money,
		Status:            domain.PaymentOrderStatusInitiated,
		InstrumentCode:    "RICE_V1",
		PaymentAttributes: map[string]string{"grade": "B"},
		CorrelationID:     uuid.New().String(),
		IdempotencyKey:    uuid.New().String(),
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		Version:           1,
	}

	// Save to repository
	err = repo.Create(context.Background(), po)
	require.NoError(t, err)

	// Execute orchestration
	orchestrator.Orchestrate(context.Background(), po)

	// Wait for the saga to complete (payment should transition to EXECUTING)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			updated, _ := repo.FindByID(context.Background(), po.ID)
			return updated != nil && updated.Status == domain.PaymentOrderStatusExecuting
		})
	require.NoError(t, err, "Payment order should transition to EXECUTING")

	// Verify bucket_id was persisted in the payment order
	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, "RICE_V1:B", updated.BucketID, "bucket_id should be saved in payment order")
}

func TestPaymentOrchestrator_EvaluateBucketID_CELEvaluationFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewMockRepository()
	refClient := NewMockReferenceDataClient()
	refClient.AddInstrument("RICE_V1", &InstrumentInfo{
		Code:                     "RICE_V1",
		Version:                  1,
		FungibilityKeyExpression: `attributes.grade`, // requires grade attribute
	})

	orchestrator, err := NewPaymentOrchestrator(PaymentOrchestratorConfig{
		Logger:              logger,
		Repo:                repo,
		ReferenceDataClient: refClient,
	})
	require.NoError(t, err)

	po := &domain.PaymentOrder{
		ID:                uuid.New(),
		InstrumentCode:    "RICE_V1",
		PaymentAttributes: map[string]string{}, // missing "grade" attribute - will cause no_such_key error
	}

	// CEL evaluation failure should gracefully degrade to default bucket (empty string)
	bucketID, err := orchestrator.evaluateBucketID(context.Background(), po)
	require.NoError(t, err, "CEL evaluation failure should gracefully degrade, not error")
	assert.Equal(t, "", bucketID, "should return empty bucket ID on CEL evaluation failure")
}
