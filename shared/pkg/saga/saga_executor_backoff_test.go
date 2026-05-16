package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleTransientFailure_SetsNextRetryAt verifies that a transient failure
// records next_retry_at on the saga before releasing the lease.
func TestHandleTransientFailure_SetsNextRetryAt(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      0,
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, nil, nil, nil)

	before := time.Now()
	transientErr := errors.New("connection timeout")
	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		1,
		"post_entries",
		transientErr,
		5,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, FailureActionRetry, result.Action)

	// next_retry_at must be set to a future time.
	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	require.NotNil(t, updated.NextRetryAt, "next_retry_at must be set after transient failure")
	assert.True(t, updated.NextRetryAt.After(before),
		"next_retry_at (%s) must be after request start (%s)", updated.NextRetryAt, before)
}

// TestHandleTransientFailure_UsesGlobalConfig verifies the executor reads
// RetryBaseDelay / RetryMaxDelay from the ClaimService config when present.
func TestHandleTransientFailure_UsesGlobalConfig(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      0,
	}
	instanceRepo.Add(instance)

	// Build a ClaimService with custom retry bounds. The base delay floor is
	// high enough that the smallest possible delay (base + 0 jitter) is well
	// above any plausible test machine clock skew.
	config := &ClaimConfig{
		LeaseDuration:  5 * time.Minute,
		BatchSize:      10,
		MaxJitterMS:    0,
		MaxReplays:     DefaultMaxReplays,
		PodID:          "test-pod",
		RetryBaseDelay: 10 * time.Second,
		RetryMaxDelay:  1 * time.Hour,
	}
	// nil db is acceptable: handleTransientFailure only reads config off the service.
	executor := NewSagaExecutor(instanceRepo, nil, nil, &ClaimService{config: config})

	before := time.Now()
	transientErr := errors.New("timeout")
	_, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		0,
		"flaky_step",
		transientErr,
		5,
	)
	require.NoError(t, err)

	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	require.NotNil(t, updated.NextRetryAt)

	// Replay count 0 with base=10s should give delay in [10s, 20s).
	delay := updated.NextRetryAt.Sub(before)
	assert.GreaterOrEqual(t, delay, 10*time.Second,
		"backoff delay (%s) should be >= base 10s", delay)
	assert.Less(t, delay, 21*time.Second,
		"backoff delay (%s) should be < base*2 + tolerance", delay)
}

// TestHandleTransientFailure_DelayGrowsWithReplayCount verifies the exponential
// component: a higher replay_count produces a longer backoff window.
func TestHandleTransientFailure_DelayGrowsWithReplayCount(t *testing.T) {
	makeExecutor := func(replayCount int) (*MockSagaInstanceRepositoryForExecutor, uuid.UUID, *SagaExecutor) {
		repo := NewMockSagaInstanceRepositoryForExecutor()
		id := uuid.New()
		repo.Add(&SagaInstance{
			ID:               id,
			SagaDefinitionID: uuid.New(),
			CorrelationID:    uuid.New(),
			Status:           SagaStatusRunning,
			ReplayCount:      replayCount,
		})
		config := &ClaimConfig{
			RetryBaseDelay: 1 * time.Second,
			RetryMaxDelay:  1 * time.Hour,
			MaxReplays:     20,
		}
		return repo, id, NewSagaExecutor(repo, nil, nil, &ClaimService{config: config})
	}

	transientErr := errors.New("timeout")

	repo0, id0, ex0 := makeExecutor(0)
	before0 := time.Now()
	_, err := ex0.HandleStepFailure(context.Background(), id0, 0, "step", transientErr, 20)
	require.NoError(t, err)
	delay0 := repo0.instances[id0].NextRetryAt.Sub(before0)

	repo5, id5, ex5 := makeExecutor(5)
	before5 := time.Now()
	_, err = ex5.HandleStepFailure(context.Background(), id5, 0, "step", transientErr, 20)
	require.NoError(t, err)
	delay5 := repo5.instances[id5].NextRetryAt.Sub(before5)

	// replay=0 -> 1-2s, replay=5 -> ~32-33s. The latter must be substantially larger.
	assert.Greater(t, delay5, delay0*4,
		"replay=5 delay (%s) should be substantially larger than replay=0 delay (%s)", delay5, delay0)
}

// TestHandleTransientFailure_AtMaxReplaysGoesToManualIntervention verifies the
// existing zombie-detection path is still reached and that the success branch
// (set next_retry_at) is NOT taken.
func TestHandleTransientFailure_AtMaxReplaysGoesToManualIntervention(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      5, // at max
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, nil, nil, nil)

	result, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		0,
		"step",
		errors.New("timeout"),
		5,
	)
	require.NoError(t, err)
	assert.Equal(t, FailureActionManualIntervention, result.Action)

	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Nil(t, updated.NextRetryAt,
		"zombie path must not set next_retry_at - saga is going to FAILED_MANUAL_INTERVENTION")
}

// TestHandleTransientFailure_PerHandlerRetryPolicyOverridesGlobal verifies that
// a handler-level retry policy registered on HandlerMetadata takes precedence
// over the global ClaimConfig defaults.
func TestHandleTransientFailure_PerHandlerRetryPolicyOverridesGlobal(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()

	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      0,
	}
	instanceRepo.Add(instance)

	// Register a handler with a 30s base delay - much larger than the 1s global.
	registry := NewHandlerRegistry()
	require.NoError(t, registry.RegisterWithMetadata(
		"external.slow_handler",
		func(_ *StarlarkContext, _ map[string]any) (any, error) { return nil, nil },
		&HandlerMetadata{
			RetryPolicy: &RetryPolicy{
				BaseDelay: 30 * time.Second,
				MaxDelay:  1 * time.Hour,
			},
		},
	))

	config := &ClaimConfig{
		RetryBaseDelay: 1 * time.Second, // small global default
		RetryMaxDelay:  5 * time.Minute,
	}
	executor := NewSagaExecutor(instanceRepo, nil, registry, &ClaimService{config: config})

	before := time.Now()
	_, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		0,
		"external.slow_handler",
		errors.New("upstream timeout"),
		5,
	)
	require.NoError(t, err)

	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	require.NotNil(t, updated.NextRetryAt)
	delay := updated.NextRetryAt.Sub(before)
	// Per-handler base 30s, replay 0 -> delay in [30s, 60s).
	assert.GreaterOrEqual(t, delay, 30*time.Second,
		"per-handler base delay must override global 1s default")
}

// TestHandleTransientFailure_NextRetryAtPersistenceFails verifies that an
// infrastructure error when writing next_retry_at propagates back to the
// caller rather than being logged and ignored. Silently continuing would
// release the lease without a recorded backoff window, allowing immediate
// re-claim and defeating the backoff guarantee.
func TestHandleTransientFailure_NextRetryAtPersistenceFails(t *testing.T) {
	instanceRepo := &failingNextRetryAtRepo{
		MockSagaInstanceRepositoryForExecutor: NewMockSagaInstanceRepositoryForExecutor(),
		failErr:                               errors.New("transient db outage"),
	}
	sagaID := uuid.New()
	instanceRepo.Add(&SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      0,
	})

	executor := NewSagaExecutor(instanceRepo, nil, nil, nil)

	_, err := executor.HandleStepFailure(
		context.Background(),
		sagaID,
		0,
		"step",
		errors.New("timeout"),
		5,
	)
	require.Error(t, err, "infra error during UpdateNextRetryAt must propagate")
	assert.Contains(t, err.Error(), "next_retry_at",
		"error message should identify the failing operation")
}

// failingNextRetryAtRepo wraps the standard mock and forces UpdateNextRetryAt
// to fail with a configurable error.
type failingNextRetryAtRepo struct {
	*MockSagaInstanceRepositoryForExecutor
	failErr error
}

func (r *failingNextRetryAtRepo) UpdateNextRetryAt(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return r.failErr
}

// TestProcessStepResult_SuccessClearsBackoff verifies that completing a step
// resets BOTH replay_count and next_retry_at in one operation.
func TestProcessStepResult_SuccessClearsBackoff(t *testing.T) {
	instanceRepo := NewMockSagaInstanceRepositoryForExecutor()

	sagaID := uuid.New()
	prevRetry := time.Now().Add(30 * time.Second)
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      3,
		NextRetryAt:      &prevRetry,
	}
	instanceRepo.Add(instance)

	executor := NewSagaExecutor(instanceRepo, nil, nil, nil)

	_, err := executor.ProcessStepResult(context.Background(), instance, 1, "step", nil, nil, 5)
	require.NoError(t, err)

	updated, _ := instanceRepo.FindByID(context.Background(), sagaID)
	assert.Equal(t, 0, updated.ReplayCount, "replay_count must reset on step success")
	assert.Nil(t, updated.NextRetryAt, "next_retry_at must clear on step success")
}
