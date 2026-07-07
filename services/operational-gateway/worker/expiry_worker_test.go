package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helpers ---

// expiredInstruction creates a PENDING instruction with an expires_at in the past.
func expiredInstruction() *domain.Instruction {
	past := time.Now().Add(-1 * time.Minute)
	return &domain.Instruction{
		ID:                   uuid.New(),
		TenantID:             testTenantID(),
		InstructionType:      "payment.initiate",
		ProviderConnectionID: "conn-123",
		Payload:              map[string]any{"amount": "100.00"},
		Priority:             domain.PriorityNormal,
		Status:               domain.InstructionStatusPending,
		ExpiresAt:            &past,
		MaxAttempts:          3,
		AttemptCount:         0,
		Attempts:             []domain.InstructionAttempt{},
		Version:              1,
		CreatedAt:            time.Now().Add(-2 * time.Minute),
		UpdatedAt:            time.Now().Add(-2 * time.Minute),
	}
}

// retryingExpiredInstruction creates a RETRYING instruction with an expires_at in the past.
func retryingExpiredInstruction() *domain.Instruction {
	instr := expiredInstruction()
	instr.Status = domain.InstructionStatusRetrying
	instr.AttemptCount = 1
	return instr
}

// stuckDispatchingInstruction creates a DISPATCHING instruction that has exceeded its lease.
func stuckDispatchingInstruction() *domain.Instruction {
	stale := time.Now().Add(-30 * time.Minute)
	return &domain.Instruction{
		ID:                   uuid.New(),
		TenantID:             testTenantID(),
		InstructionType:      "kyc.verify",
		ProviderConnectionID: "conn-123",
		Payload:              map[string]any{"amount": "100.00"},
		Priority:             domain.PriorityNormal,
		Status:               domain.InstructionStatusDispatching,
		DispatchedAt:         &stale,
		MaxAttempts:          3,
		AttemptCount:         1,
		Attempts:             []domain.InstructionAttempt{},
		Version:              1,
		CreatedAt:            time.Now().Add(-1 * time.Hour),
		UpdatedAt:            stale,
	}
}

// mockExpiryRepo is a minimal mock satisfying ports.InstructionRepository for expiry worker tests.
type mockExpiryRepo struct {
	mockInstructionRepo
	findExpired      func(ctx context.Context, batchSize int) ([]*domain.Instruction, error)
	findExpiredCalls int
	findStuck        func(ctx context.Context, leaseTimeout time.Duration, batchSize int) ([]*domain.Instruction, error)
	findStuckCalls   int
	lastLeaseTimeout time.Duration
}

func (m *mockExpiryRepo) FindExpired(ctx context.Context, batchSize int) ([]*domain.Instruction, error) {
	m.mu.Lock()
	m.findExpiredCalls++
	m.mu.Unlock()
	if m.findExpired != nil {
		return m.findExpired(ctx, batchSize)
	}
	return nil, nil
}

func (m *mockExpiryRepo) FindStuckDispatching(ctx context.Context, leaseTimeout time.Duration, batchSize int) ([]*domain.Instruction, error) {
	m.mu.Lock()
	m.findStuckCalls++
	m.lastLeaseTimeout = leaseTimeout
	m.mu.Unlock()
	if m.findStuck != nil {
		return m.findStuck(ctx, leaseTimeout, batchSize)
	}
	return nil, nil
}

func (m *mockExpiryRepo) getFindExpiredCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.findExpiredCalls
}

func (m *mockExpiryRepo) getLastLeaseTimeout() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastLeaseTimeout
}

// --- Tests ---

func TestNewExpiryWorker_AppliesDefaults(t *testing.T) {
	repo := &mockExpiryRepo{}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)

	assert.Equal(t, defaultExpiryScanInterval, w.config.ScanInterval)
	assert.Equal(t, defaultExpiryBatchSize, w.config.BatchSize)
	assert.Equal(t, defaultLeaseTimeout, w.config.LeaseTimeout)
	assert.NotNil(t, w.logger)
}

// TestExpiryWorker_ReapStuckDispatching_ReclaimsToRetrying verifies a DISPATCHING instruction
// whose lease has expired and which still has attempts remaining is reclaimed to RETRYING and
// made eligible for immediate re-dispatch.
func TestExpiryWorker_ReapStuckDispatching_ReclaimsToRetrying(t *testing.T) {
	stuck := stuckDispatchingInstruction()
	stuck.AttemptCount = 1
	stuck.MaxAttempts = 3

	repo := &mockExpiryRepo{
		findStuck: func(_ context.Context, _ time.Duration, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{stuck}, nil
		},
	}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)

	w.reapStuckDispatching(context.Background())

	saved := repo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusRetrying, saved[0].Status)
	assert.Nil(t, saved[0].NextRetryAt, "reclaimed instruction should retry immediately")
	assert.Equal(t, defaultLeaseTimeout, repo.getLastLeaseTimeout())
}

// TestExpiryWorker_ReapStuckDispatching_ExhaustedMarksFailed verifies a stuck DISPATCHING
// instruction with no remaining attempts is marked FAILED rather than retried forever.
func TestExpiryWorker_ReapStuckDispatching_ExhaustedMarksFailed(t *testing.T) {
	stuck := stuckDispatchingInstruction()
	stuck.AttemptCount = 3
	stuck.MaxAttempts = 3

	repo := &mockExpiryRepo{
		findStuck: func(_ context.Context, _ time.Duration, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{stuck}, nil
		},
	}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)

	w.reapStuckDispatching(context.Background())

	saved := repo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusFailed, saved[0].Status)
	assert.Equal(t, leaseExpiredReason, saved[0].FailureReason)
}

// TestExpiryWorker_ReapStuckDispatching_NoStuckRowsNoSave verifies the reaper does not persist
// anything when there are no stuck DISPATCHING instructions.
func TestExpiryWorker_ReapStuckDispatching_NoStuckRowsNoSave(t *testing.T) {
	repo := &mockExpiryRepo{
		findStuck: func(_ context.Context, _ time.Duration, _ int) ([]*domain.Instruction, error) {
			return nil, nil
		},
	}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)

	w.reapStuckDispatching(context.Background())

	assert.Empty(t, repo.getSavedInstructions())
}

// TestExpiryWorker_ReclaimInstruction_SkipsNonDispatching verifies an instruction that is no
// longer DISPATCHING (e.g. concurrently progressed) is left untouched.
func TestExpiryWorker_ReclaimInstruction_SkipsNonDispatching(t *testing.T) {
	repo := &mockExpiryRepo{}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)

	instr := stuckDispatchingInstruction()
	instr.Status = domain.InstructionStatusDelivered

	outcome := w.reclaimInstruction(context.Background(), instr)

	assert.Equal(t, reclaimSkipped, outcome)
	assert.Empty(t, repo.getSavedInstructions())
}

func TestNewExpiryWorker_RespectsCustomConfig(t *testing.T) {
	repo := &mockExpiryRepo{}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{
		ScanInterval: 10 * time.Second,
		BatchSize:    50,
	}, nil)

	assert.Equal(t, 10*time.Second, w.config.ScanInterval)
	assert.Equal(t, 50, w.config.BatchSize)
}

func TestExpiryWorker_StartAndStop(t *testing.T) {
	repo := &mockExpiryRepo{}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{ScanInterval: 50 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	// Wait for at least one scan cycle.
	err := await.AtMost(2 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return repo.getFindExpiredCalls() >= 1
	})
	require.NoError(t, err)

	w.Stop()

	// Idempotent Stop.
	w.Stop()
}

func TestExpiryWorker_StartIdempotent(t *testing.T) {
	repo := &mockExpiryRepo{}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{ScanInterval: 50 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)
	w.Start(ctx) // second call is a no-op

	err := await.AtMost(2 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return repo.getFindExpiredCalls() >= 1
	})
	require.NoError(t, err)

	w.Stop()
}

func TestExpiryWorker_StopsOnContextCancel(t *testing.T) {
	repo := &mockExpiryRepo{}
	w := NewExpiryWorker(repo, ExpiryWorkerConfig{ScanInterval: 50 * time.Millisecond}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	err := await.AtMost(2 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return repo.getFindExpiredCalls() >= 1
	})
	require.NoError(t, err)

	cancel()

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestScanAndExpire_ExpiresInstructions(t *testing.T) {
	instr := expiredInstruction()

	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{instr}, nil
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	w.scanAndExpire(context.Background())

	saved := repo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusExpired, saved[0].Status)
	assert.NotNil(t, saved[0].CompletedAt)
}

func TestScanAndExpire_ExpiresRetryingInstructions(t *testing.T) {
	instr := retryingExpiredInstruction()

	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{instr}, nil
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	w.scanAndExpire(context.Background())

	saved := repo.getSavedInstructions()
	require.Len(t, saved, 1)
	assert.Equal(t, domain.InstructionStatusExpired, saved[0].Status)
}

func TestScanAndExpire_SkipsTerminalInstructions(t *testing.T) {
	// Simulate an instruction that reached FAILED between query and processing.
	instr := expiredInstruction()
	instr.Status = domain.InstructionStatusFailed
	instr.FailureReason = "some error"
	instr.ErrorCode = "SOME_ERROR"

	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{instr}, nil
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	w.scanAndExpire(context.Background())

	// Nothing saved — skipped because already terminal.
	assert.Equal(t, 0, repo.getSaveCalls())
}

func TestScanAndExpire_SkipsAlreadyExpired(t *testing.T) {
	instr := expiredInstruction()
	instr.Status = domain.InstructionStatusExpired

	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{instr}, nil
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	w.scanAndExpire(context.Background())

	assert.Equal(t, 0, repo.getSaveCalls())
}

func TestScanAndExpire_EmptyBatch_NoOp(t *testing.T) {
	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return nil, nil
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	w.scanAndExpire(context.Background())

	assert.Equal(t, 0, repo.getSaveCalls())
}

func TestScanAndExpire_FindExpiredError_DoesNotPanic(t *testing.T) {
	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return nil, errors.New("db connection lost")
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	// Should not panic.
	w.scanAndExpire(context.Background())
	assert.Equal(t, 0, repo.getSaveCalls())
}

func TestScanAndExpire_SaveError_ContinuesProcessingRemainder(t *testing.T) {
	instr1 := expiredInstruction()
	instr2 := expiredInstruction()

	saveCount := 0
	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{instr1, instr2}, nil
		},
	}
	// Override save to fail on first call, succeed on second.
	repo.save = func(_ context.Context, _ *domain.Instruction, _ string) error {
		saveCount++
		if saveCount == 1 {
			return errors.New("transient write error")
		}
		return nil
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	w.scanAndExpire(context.Background())

	// Both instructions attempted; first save failed but second succeeded.
	assert.Equal(t, 2, saveCount)
}

func TestScanAndExpire_PassesBatchSizeToFindExpired(t *testing.T) {
	var capturedBatchSize int
	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, batchSize int) ([]*domain.Instruction, error) {
			capturedBatchSize = batchSize
			return nil, nil
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{
		BatchSize:    42,
		ScanInterval: 50 * time.Millisecond,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	err := await.AtMost(2 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return repo.getFindExpiredCalls() >= 1
	})
	require.NoError(t, err)

	w.Stop()
	assert.Equal(t, 42, capturedBatchSize)
}

func TestScanAndExpire_ProcessesMultipleInstructions(t *testing.T) {
	instr1 := expiredInstruction()
	instr2 := retryingExpiredInstruction()
	instr3 := expiredInstruction()

	repo := &mockExpiryRepo{
		findExpired: func(_ context.Context, _ int) ([]*domain.Instruction, error) {
			return []*domain.Instruction{instr1, instr2, instr3}, nil
		},
	}

	w := NewExpiryWorker(repo, ExpiryWorkerConfig{}, nil)
	w.scanAndExpire(context.Background())

	saved := repo.getSavedInstructions()
	require.Len(t, saved, 3)
	for _, s := range saved {
		assert.Equal(t, domain.InstructionStatusExpired, s.Status)
	}
}
