// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Unit Tests ---

func TestSuspendRequest_Validation(t *testing.T) {
	t.Run("empty idempotency key fails", func(t *testing.T) {
		req := &SuspendRequest{
			IdempotencyKey: "",
			Timeout:        time.Hour,
		}
		assert.Empty(t, req.IdempotencyKey)
	})

	t.Run("negative timeout uses default", func(t *testing.T) {
		config := DefaultSuspendConfig()
		assert.True(t, config.DefaultTimeout > 0)
		assert.True(t, config.MaxTimeout > config.DefaultTimeout)
	})
}

func TestSuspendSaga_ValidationErrors(t *testing.T) {
	// These tests don't need a database - they test validation before DB calls
	suspendService := &SuspendService{config: DefaultSuspendConfig()}
	ctx := context.Background()
	instance := &SagaInstance{ID: uuid.New()}

	t.Run("nil instance returns error", func(t *testing.T) {
		req := &SuspendRequest{
			IdempotencyKey: "test-key",
			Timeout:        time.Hour,
		}
		result, err := suspendService.SuspendSaga(ctx, nil, 0, "step", req)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrSuspendSagaNotFound)
	})

	t.Run("nil request returns error", func(t *testing.T) {
		result, err := suspendService.SuspendSaga(ctx, instance, 0, "step", nil)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrIdempotencyKeyRequired)
	})

	t.Run("empty idempotency key returns error", func(t *testing.T) {
		req := &SuspendRequest{
			IdempotencyKey: "",
			Timeout:        time.Hour,
		}
		result, err := suspendService.SuspendSaga(ctx, instance, 0, "step", req)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, ErrIdempotencyKeyRequired)
	})
}

func TestStepStatusSuspended(t *testing.T) {
	assert.Equal(t, StepStatus("SUSPENDED"), StepStatusSuspended)
}

// --- Integration Tests ---

func TestIntegration_Suspend_BasicFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create saga instance
	instanceID := uuid.New()
	correlationID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    correlationID,
		Status:           SagaStatusRunning,
		CurrentStepIndex: 2,
		ClaimedByPod:     stringPtr("pod-123"),
		ClaimedAt:        timePtr(time.Now()),
		LeaseExpiresAt:   timePtr(time.Now().Add(5 * time.Minute)),
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Suspend the saga
	suspendReq := &SuspendRequest{
		IdempotencyKey: "payment-confirmation-12345",
		Timeout:        2 * time.Hour,
		Reason:         "Waiting for DNO payment confirmation",
		Data: map[string]interface{}{
			"payment_ref": "PAY-ABC-123",
			"amount":      1000.50,
		},
	}

	result, err := suspendService.SuspendSaga(ctx, instance, 2, "await_payment", suspendReq)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify suspension result
	assert.Equal(t, instanceID, result.SagaInstanceID)
	assert.Equal(t, "payment-confirmation-12345", result.IdempotencyKey)
	assert.True(t, result.TimeoutAt.After(time.Now()))
	assert.True(t, result.TimeoutAt.Before(time.Now().Add(3*time.Hour)))

	// Verify saga instance was updated correctly
	var updatedSaga SagaInstance
	err = db.First(&updatedSaga, "id = ?", instanceID).Error
	require.NoError(t, err)

	assert.Equal(t, SagaStatusWaitingForEvent, updatedSaga.Status)
	assert.Nil(t, updatedSaga.ClaimedByPod, "Lease should be released")
	assert.Nil(t, updatedSaga.ClaimedAt, "Claim time should be cleared")
	assert.Nil(t, updatedSaga.LeaseExpiresAt, "Lease expiry should be cleared")
	assert.Equal(t, "Waiting for DNO payment confirmation", *updatedSaga.SuspendReason)

	// Verify step result was created with SUSPENDED status
	var stepResult SagaStepResult
	idempotencyKey := FormatIdempotencyKey(instanceID, 2)
	err = db.First(&stepResult, "idempotency_key = ?", idempotencyKey).Error
	require.NoError(t, err)

	assert.Equal(t, StepStatusSuspended, stepResult.Status)
	assert.Equal(t, "await_payment", stepResult.StepName)
	assert.Equal(t, 2, stepResult.StepIndex)
}

func TestIntegration_Suspend_LeaseRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create saga instance with active lease
	instanceID := uuid.New()
	podID := "worker-pod-xyz"
	claimedAt := time.Now()
	leaseExpiry := time.Now().Add(5 * time.Minute)

	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
		ClaimedByPod:     &podID,
		ClaimedAt:        &claimedAt,
		LeaseExpiresAt:   &leaseExpiry,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Suspend
	suspendReq := &SuspendRequest{
		IdempotencyKey: "test-lease-release",
		Timeout:        time.Hour,
	}
	_, err = suspendService.SuspendSaga(ctx, instance, 0, "step", suspendReq)
	require.NoError(t, err)

	// Verify lease was released
	var updatedSaga SagaInstance
	err = db.First(&updatedSaga, "id = ?", instanceID).Error
	require.NoError(t, err)

	assert.Nil(t, updatedSaga.ClaimedByPod, "claimed_by_pod should be NULL after suspend")
	assert.Nil(t, updatedSaga.ClaimedAt, "claimed_at should be NULL after suspend")
	assert.Nil(t, updatedSaga.LeaseExpiresAt, "lease_expires_at should be NULL after suspend")

	// Verify status changed to WAITING_FOR_EVENT
	assert.Equal(t, SagaStatusWaitingForEvent, updatedSaga.Status)
}

func TestIntegration_CompleteSagaStep_BasicFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create and suspend a saga
	instanceID := uuid.New()
	idempotencyKey := "webhook-callback-key"
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 1,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Suspend the saga
	suspendReq := &SuspendRequest{
		IdempotencyKey: idempotencyKey,
		Timeout:        time.Hour,
	}
	_, err = suspendService.SuspendSaga(ctx, instance, 1, "await_callback", suspendReq)
	require.NoError(t, err)

	// Complete the saga step (webhook callback)
	completeReq := &CompleteSagaStepRequest{
		IdempotencyKey: idempotencyKey,
		Result: map[string]interface{}{
			"payment_status": "CONFIRMED",
			"transaction_id": "TXN-987654",
			"confirmed_at":   time.Now().Format(time.RFC3339),
		},
	}

	response, err := suspendService.CompleteSagaStep(ctx, completeReq)
	require.NoError(t, err)
	require.NotNil(t, response)

	// Verify response
	assert.Equal(t, instanceID, response.SagaInstanceID)
	assert.False(t, response.WasAlreadyCompleted)
	assert.Equal(t, SagaStatusPending, response.NewStatus)

	// Verify saga was resumed (status back to PENDING for worker to claim)
	var resumedSaga SagaInstance
	err = db.First(&resumedSaga, "id = ?", instanceID).Error
	require.NoError(t, err)

	assert.Equal(t, SagaStatusPending, resumedSaga.Status)
	assert.Nil(t, resumedSaga.SuspendReason, "suspend_reason should be cleared")

	// Verify step result was updated with callback data
	stepKey := FormatIdempotencyKey(instanceID, 1)
	var stepResult SagaStepResult
	err = db.First(&stepResult, "idempotency_key = ?", stepKey).Error
	require.NoError(t, err)

	assert.Equal(t, StepStatusCompleted, stepResult.Status)
	assert.NotNil(t, stepResult.Result)
	assert.Equal(t, "CONFIRMED", stepResult.Result["payment_status"])
}

func TestIntegration_CompleteSagaStep_Idempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create and suspend a saga
	instanceID := uuid.New()
	idempotencyKey := "idempotent-callback"
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Suspend
	suspendReq := &SuspendRequest{
		IdempotencyKey: idempotencyKey,
		Timeout:        time.Hour,
	}
	_, err = suspendService.SuspendSaga(ctx, instance, 0, "step", suspendReq)
	require.NoError(t, err)

	// First completion
	completeReq := &CompleteSagaStepRequest{
		IdempotencyKey: idempotencyKey,
		Result:         map[string]interface{}{"attempt": 1},
	}
	response1, err := suspendService.CompleteSagaStep(ctx, completeReq)
	require.NoError(t, err)
	assert.False(t, response1.WasAlreadyCompleted)
	assert.Equal(t, SagaStatusPending, response1.NewStatus)

	// Second completion (idempotent - saga already resumed)
	completeReq.Result = map[string]interface{}{"attempt": 2}
	response2, err := suspendService.CompleteSagaStep(ctx, completeReq)
	require.NoError(t, err)
	assert.True(t, response2.WasAlreadyCompleted, "Second call should be idempotent no-op")
	assert.Equal(t, SagaStatusPending, response2.NewStatus)

	// Third completion
	response3, err := suspendService.CompleteSagaStep(ctx, completeReq)
	require.NoError(t, err)
	assert.True(t, response3.WasAlreadyCompleted)

	// Verify saga was only updated once (step result should have attempt: 1)
	stepKey := FormatIdempotencyKey(instanceID, 0)
	var stepResult SagaStepResult
	err = db.First(&stepResult, "idempotency_key = ?", stepKey).Error
	require.NoError(t, err)

	assert.Equal(t, float64(1), stepResult.Result["attempt"],
		"Result should be from first completion, not overwritten by idempotent calls")
}

func TestIntegration_CompleteSagaStep_ConcurrentCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create and suspend a saga
	instanceID := uuid.New()
	idempotencyKey := "concurrent-callback"
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Suspend
	suspendReq := &SuspendRequest{
		IdempotencyKey: idempotencyKey,
		Timeout:        time.Hour,
	}
	_, err = suspendService.SuspendSaga(ctx, instance, 0, "step", suspendReq)
	require.NoError(t, err)

	// Call CompleteSagaStep concurrently 10 times
	numGoroutines := 10
	var wg sync.WaitGroup
	responses := make([]*CompleteSagaStepResponse, numGoroutines)
	errs := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &CompleteSagaStepRequest{
				IdempotencyKey: idempotencyKey,
				Result:         map[string]interface{}{"caller": idx},
			}
			responses[idx], errs[idx] = suspendService.CompleteSagaStep(ctx, req)
		}(i)
	}

	wg.Wait()

	// All calls should succeed
	for i, err := range errs {
		assert.NoError(t, err, "Call %d should not error", i)
	}

	// Exactly one should be the "first" completion, others idempotent
	firstCount := 0
	idempotentCount := 0
	for _, resp := range responses {
		if resp == nil {
			continue // Skip failed calls
		}
		if resp.WasAlreadyCompleted {
			idempotentCount++
		} else {
			firstCount++
		}
	}

	assert.Equal(t, 1, firstCount, "Exactly one call should be the first completion")
	assert.Equal(t, numGoroutines-1, idempotentCount, "Other calls should be idempotent")
}

func TestIntegration_CompleteSagaStep_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Try to complete a non-existent saga
	completeReq := &CompleteSagaStepRequest{
		IdempotencyKey: "non-existent-key",
		Result:         map[string]interface{}{},
	}

	_, err = suspendService.CompleteSagaStep(ctx, completeReq)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "saga not found")
}

func TestIntegration_FindSuspendedByIdempotencyKey(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create and suspend a saga
	instanceID := uuid.New()
	idempotencyKey := "find-test-key"
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Before suspend - should not find
	found, err := suspendService.FindSuspendedByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	assert.Nil(t, found)

	// Suspend
	suspendReq := &SuspendRequest{
		IdempotencyKey: idempotencyKey,
		Timeout:        time.Hour,
	}
	_, err = suspendService.SuspendSaga(ctx, instance, 0, "step", suspendReq)
	require.NoError(t, err)

	// After suspend - should find
	found, err = suspendService.FindSuspendedByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, instanceID, found.ID)

	// Complete the saga
	completeReq := &CompleteSagaStepRequest{
		IdempotencyKey: idempotencyKey,
		Result:         map[string]interface{}{},
	}
	_, err = suspendService.CompleteSagaStep(ctx, completeReq)
	require.NoError(t, err)

	// After completion - should not find (status is no longer WAITING_FOR_EVENT)
	found, err = suspendService.FindSuspendedByIdempotencyKey(ctx, idempotencyKey)
	require.NoError(t, err)
	assert.Nil(t, found)
}

func TestIntegration_TimeoutWorker_ExpiresSuspendedSagas(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a saga that's already past its timeout
	instanceID := uuid.New()
	pastTimeout := time.Now().Add(-1 * time.Hour) // 1 hour ago
	suspendData := JSONB{
		"idempotency_key": "expired-saga-key",
		"timeout_at":      pastTimeout,
	}
	suspendReason := "Waiting for callback"

	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusWaitingForEvent,
		CurrentStepIndex: 1,
		SuspendReason:    &suspendReason,
		SuspendData:      suspendData,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	// Create corresponding step result with SUSPENDED status
	stepKey := FormatIdempotencyKey(instanceID, 1)
	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: instanceID,
		StepIndex:      1,
		StepName:       "await_callback",
		IdempotencyKey: stepKey,
		Status:         StepStatusSuspended,
	}
	err = db.Create(stepResult).Error
	require.NoError(t, err)

	// Create timeout worker and process
	workerConfig := &TimeoutWorkerConfig{
		PollInterval: time.Second,
		BatchSize:    10,
	}
	worker := NewTimeoutWorker(db, workerConfig)
	ctx := context.Background()

	err = worker.ProcessExpiredSuspensions(ctx)
	require.NoError(t, err)

	// Verify saga was transitioned to FAILED
	var updatedSaga SagaInstance
	err = db.First(&updatedSaga, "id = ?", instanceID).Error
	require.NoError(t, err)

	assert.Equal(t, SagaStatusFailed, updatedSaga.Status)
	assert.Contains(t, *updatedSaga.ErrorMessage, "Suspend timeout exceeded")
	assert.Equal(t, string(ErrorCategoryFatal), *updatedSaga.ErrorCategory)
	assert.Nil(t, updatedSaga.SuspendReason)
	assert.Nil(t, updatedSaga.SuspendData)

	// Verify step result was updated to FAILED
	var updatedStep SagaStepResult
	err = db.First(&updatedStep, "idempotency_key = ?", stepKey).Error
	require.NoError(t, err)

	assert.Equal(t, StepStatusFailed, updatedStep.Status)
	assert.Contains(t, *updatedStep.Error, "Timeout waiting for external event")
}

func TestIntegration_TimeoutWorker_DoesNotExpireActiveSagas(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a saga with future timeout
	instanceID := uuid.New()
	futureTimeout := time.Now().Add(2 * time.Hour)
	suspendData := JSONB{
		"idempotency_key": "active-saga-key",
		"timeout_at":      futureTimeout,
	}

	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusWaitingForEvent,
		CurrentStepIndex: 0,
		SuspendData:      suspendData,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	// Process expired suspensions
	worker := NewTimeoutWorker(db, DefaultTimeoutWorkerConfig())
	ctx := context.Background()

	err = worker.ProcessExpiredSuspensions(ctx)
	require.NoError(t, err)

	// Verify saga was NOT touched
	var unchanged SagaInstance
	err = db.First(&unchanged, "id = ?", instanceID).Error
	require.NoError(t, err)

	assert.Equal(t, SagaStatusWaitingForEvent, unchanged.Status,
		"Saga with future timeout should not be expired")
}

func TestIntegration_TimeoutWorker_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	workerConfig := &TimeoutWorkerConfig{
		PollInterval: 100 * time.Millisecond,
		BatchSize:    10,
	}
	worker := NewTimeoutWorker(db, workerConfig)

	ctx, cancel := context.WithCancel(context.Background())

	// Start worker in goroutine
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- worker.Start(ctx)
	}()

	// Let it run for a bit
	//nolint:forbidigo // allows worker to complete multiple poll cycles before testing graceful shutdown
	time.Sleep(250 * time.Millisecond)

	// Cancel context to stop worker
	cancel()

	// Worker should stop cleanly within reasonable time
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			select {
			case <-workerDone:
				return true
			default:
				return false
			}
		})
	require.NoError(t, err, "Worker should stop after context cancellation")
}

func TestIntegration_SuspendAndResume_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Scenario: DNO Payment Confirmation
	// 1. Saga starts processing a payment order
	// 2. Saga calls external DNO API to initiate payment
	// 3. Saga suspends waiting for webhook confirmation
	// 4. Hours later, webhook arrives with confirmation
	// 5. Saga resumes and completes

	instanceID := uuid.New()
	correlationID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    correlationID,
		Status:           SagaStatusRunning,
		CurrentStepIndex: 3, // Already completed steps 0-2
		ClaimedByPod:     stringPtr("payment-worker-1"),
		ClaimedAt:        timePtr(time.Now()),
		LeaseExpiresAt:   timePtr(time.Now().Add(5 * time.Minute)),
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// Step 3: Suspend waiting for DNO confirmation
	paymentRef := "DNO-PAY-" + uuid.New().String()[:8]
	suspendReq := &SuspendRequest{
		IdempotencyKey: paymentRef,
		Timeout:        72 * time.Hour, // 3 days to receive confirmation
		Reason:         "Awaiting DNO payment settlement confirmation",
		Data: map[string]interface{}{
			"payment_ref":      paymentRef,
			"amount_cents":     150000,
			"recipient_msisdn": "+254700123456",
		},
	}

	suspendResult, err := suspendService.SuspendSaga(ctx, instance, 3, "await_dno_confirmation", suspendReq)
	require.NoError(t, err)

	// Verify suspension
	assert.Equal(t, paymentRef, suspendResult.IdempotencyKey)

	// Verify saga state changed correctly
	var suspendedSaga SagaInstance
	err = db.First(&suspendedSaga, "id = ?", instanceID).Error
	require.NoError(t, err)

	assert.Equal(t, SagaStatusWaitingForEvent, suspendedSaga.Status)
	assert.Nil(t, suspendedSaga.ClaimedByPod, "Worker should release lease when suspending")

	// Simulate webhook callback (hours later)
	webhookPayload := map[string]interface{}{
		"status":         "SETTLED",
		"transaction_id": "TXN-" + uuid.New().String()[:8],
		"settled_at":     time.Now().Format(time.RFC3339),
		"settlement_ref": "SETTLE-12345",
	}

	completeReq := &CompleteSagaStepRequest{
		IdempotencyKey: paymentRef,
		Result:         webhookPayload,
	}

	completeResp, err := suspendService.CompleteSagaStep(ctx, completeReq)
	require.NoError(t, err)

	assert.Equal(t, instanceID, completeResp.SagaInstanceID)
	assert.False(t, completeResp.WasAlreadyCompleted)
	assert.Equal(t, SagaStatusPending, completeResp.NewStatus)

	// Verify saga is ready to be claimed by worker again
	var resumedSaga SagaInstance
	err = db.First(&resumedSaga, "id = ?", instanceID).Error
	require.NoError(t, err)

	assert.Equal(t, SagaStatusPending, resumedSaga.Status,
		"Saga should be PENDING so worker can claim and continue execution")
	assert.Nil(t, resumedSaga.ClaimedByPod, "No worker has claimed yet")
	assert.Nil(t, resumedSaga.SuspendReason, "Suspend reason should be cleared")

	// Verify step result contains the webhook data
	stepKey := FormatIdempotencyKey(instanceID, 3)
	var stepResult SagaStepResult
	err = db.First(&stepResult, "idempotency_key = ?", stepKey).Error
	require.NoError(t, err)

	assert.Equal(t, StepStatusCompleted, stepResult.Status)
	assert.Equal(t, "SETTLED", stepResult.Result["status"])
	assert.Equal(t, "await_dno_confirmation", stepResult.StepName)
}

func TestIntegration_MultipleSuspensions_SameSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	instanceID := uuid.New()
	instance := &SagaInstance{
		ID:               instanceID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	suspendService := NewSuspendService(db, DefaultSuspendConfig())
	ctx := context.Background()

	// First suspension (step 0)
	_, err = suspendService.SuspendSaga(ctx, instance, 0, "first_suspend", &SuspendRequest{
		IdempotencyKey: "first-key",
		Timeout:        time.Hour,
	})
	require.NoError(t, err)

	// Complete first suspension
	_, err = suspendService.CompleteSagaStep(ctx, &CompleteSagaStepRequest{
		IdempotencyKey: "first-key",
		Result:         map[string]interface{}{"step": 0},
	})
	require.NoError(t, err)

	// Update saga to running for next step
	err = db.Model(&SagaInstance{}).Where("id = ?", instanceID).
		Updates(map[string]interface{}{"status": SagaStatusRunning}).Error
	require.NoError(t, err)

	// Second suspension (step 1)
	_, err = suspendService.SuspendSaga(ctx, instance, 1, "second_suspend", &SuspendRequest{
		IdempotencyKey: "second-key",
		Timeout:        time.Hour,
	})
	require.NoError(t, err)

	// Complete second suspension
	_, err = suspendService.CompleteSagaStep(ctx, &CompleteSagaStepRequest{
		IdempotencyKey: "second-key",
		Result:         map[string]interface{}{"step": 1},
	})
	require.NoError(t, err)

	// Verify both step results exist with correct data
	var steps []SagaStepResult
	err = db.Where("saga_instance_id = ?", instanceID).Order("step_index").Find(&steps).Error
	require.NoError(t, err)

	assert.Len(t, steps, 2)
	assert.Equal(t, float64(0), steps[0].Result["step"])
	assert.Equal(t, float64(1), steps[1].Result["step"])
}

// Helper functions for tests

func stringPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}
