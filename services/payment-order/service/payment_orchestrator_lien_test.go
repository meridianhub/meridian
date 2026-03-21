package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/services/payment-order/domain/testfixtures"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLock implements Lock for testing.
type mockLock struct {
	releaseErr error
}

func (m *mockLock) Release(_ context.Context) error {
	return m.releaseErr
}

// mockLockClient implements LockClient for testing.
type mockLockClient struct {
	lock Lock
	err  error
}

func (m *mockLockClient) Obtain(_ context.Context, _ string, _ time.Duration) (Lock, error) {
	return m.lock, m.err
}

// =============================================================================
// ExecuteLienWithRetry
// =============================================================================

func TestExecuteLienWithRetry_NilCurrentAccountClient(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	o := testOrchestrator(repo, nil, nil, nil)
	// Explicitly nil out the client
	o.currentAccountClient = nil

	// Should return early without panic
	o.ExecuteLienWithRetry(context.Background(), uuid.New(), "lien-123")
}

func TestExecuteLienWithRetry_Success(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	mockCA := &MockCurrentAccountClient{}
	o := testOrchestrator(repo, mockCA, nil, nil)
	o.lienExecutionRetryConfig = &sharedclients.RetryConfig{
		MaxRetries:      1,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     10 * time.Millisecond,
		Multiplier:      1.0,
	}

	o.ExecuteLienWithRetry(context.Background(), po.ID, "lien-abc")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
	assert.Equal(t, 1, updated.LienExecutionAttempts)
}

func TestExecuteLienWithRetry_FailureAfterRetries(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	mockCA := &MockCurrentAccountClient{
		executeLienErr: errors.New("service unavailable"),
	}
	o := testOrchestrator(repo, mockCA, nil, nil)
	o.lienExecutionRetryConfig = &sharedclients.RetryConfig{
		MaxRetries:      2,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      1.0,
	}

	o.ExecuteLienWithRetry(context.Background(), po.ID, "lien-fail")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusFailed, updated.LienExecutionStatus)
	assert.Contains(t, updated.LienExecutionError, "service unavailable")
}

func TestExecuteLienWithRetry_DefaultRetryConfig(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	mockCA := &MockCurrentAccountClient{}
	o := testOrchestrator(repo, mockCA, nil, nil)
	// Leave lienExecutionRetryConfig nil to trigger default path
	o.lienExecutionRetryConfig = nil

	// Use a short-lived context to avoid waiting for the full default timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	o.ExecuteLienWithRetry(ctx, po.ID, "lien-default")

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
}

// =============================================================================
// updateLienExecutionStatus
// =============================================================================

func TestUpdateLienExecutionStatus_Success_NoLock(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)
	// No lock client configured

	o.updateLienExecutionStatus(context.Background(), po.ID, 1, nil, nil, testLogger())

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
}

func TestUpdateLienExecutionStatus_Failure_RecordsError(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)

	retryErr := errors.New("all retries exhausted")
	lastErr := errors.New("connection refused")

	o.updateLienExecutionStatus(context.Background(), po.ID, 3, retryErr, lastErr, testLogger())

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusFailed, updated.LienExecutionStatus)
	assert.Contains(t, updated.LienExecutionError, "connection refused")
	assert.Equal(t, 3, updated.LienExecutionAttempts)
}

func TestUpdateLienExecutionStatus_Failure_FallsBackToRetryErr(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)

	retryErr := errors.New("retry wrapper error")

	o.updateLienExecutionStatus(context.Background(), po.ID, 2, retryErr, nil, testLogger())

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusFailed, updated.LienExecutionStatus)
	assert.Contains(t, updated.LienExecutionError, "retry wrapper error")
}

func TestUpdateLienExecutionStatus_WithLock_Success(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)
	o.lockClient = &mockLockClient{
		lock: &mockLock{},
	}

	o.updateLienExecutionStatus(context.Background(), po.ID, 1, nil, nil, testLogger())

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
}

func TestUpdateLienExecutionStatus_LockNotObtained_Returns(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)
	o.lockClient = &mockLockClient{
		err: LockNotObtainedError{},
	}

	o.updateLienExecutionStatus(context.Background(), po.ID, 1, nil, nil, testLogger())

	// Should NOT have updated the PO because lock was not obtained
	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.NotEqual(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
}

func TestUpdateLienExecutionStatus_LockError_ContinuesWithoutLock(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)
	o.lockClient = &mockLockClient{
		err: errors.New("redis connection error"),
	}

	o.updateLienExecutionStatus(context.Background(), po.ID, 1, nil, nil, testLogger())

	// Should still succeed - continues without lock
	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
}

func TestUpdateLienExecutionStatus_LockReleaseError(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)
	o.lockClient = &mockLockClient{
		lock: &mockLock{releaseErr: errors.New("release failed")},
	}

	// Should not panic despite release error
	o.updateLienExecutionStatus(context.Background(), po.ID, 1, nil, nil, testLogger())

	updated, err := repo.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
}

func TestUpdateLienExecutionStatus_FindByIDError(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	repo.findByIDErr = errors.New("db unavailable")

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)

	// Should not panic - error is logged
	o.updateLienExecutionStatus(context.Background(), uuid.New(), 1, nil, nil, testLogger())
}

func TestUpdateLienExecutionStatus_VersionConflict_Retries(t *testing.T) {
	t.Parallel()

	// Create a repo that returns version conflict on the first update, then succeeds
	repo := &versionConflictMockRepo{
		inner:           NewMockRepository(),
		conflictsRemain: 2,
		mu:              sync.Mutex{},
	}
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.inner.Create(context.Background(), po))

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)

	o.updateLienExecutionStatus(context.Background(), po.ID, 1, nil, nil, testLogger())

	updated, err := repo.inner.FindByID(context.Background(), po.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.LienExecutionStatusSucceeded, updated.LienExecutionStatus)
}

func TestUpdateLienExecutionStatus_NonRecoverableUpdateError(t *testing.T) {
	t.Parallel()

	repo := NewMockRepository()
	po := testfixtures.NewPaymentOrderInStatus(t, domain.PaymentOrderStatusCompleted)
	require.NoError(t, repo.Create(context.Background(), po))
	repo.updateErr = errors.New("disk full")

	o := testOrchestrator(repo, &MockCurrentAccountClient{}, nil, nil)

	// Should not panic - error is logged
	o.updateLienExecutionStatus(context.Background(), po.ID, 1, nil, nil, testLogger())
}

// =============================================================================
// isVersionConflict
// =============================================================================

func TestIsVersionConflict(t *testing.T) {
	t.Parallel()

	assert.True(t, isVersionConflict(persistence.ErrPaymentOrderVersionConflict))
	assert.False(t, isVersionConflict(errors.New("other error")))
	assert.False(t, isVersionConflict(nil))
}

// =============================================================================
// Helpers
// =============================================================================

// versionConflictMockRepo wraps MockRepository and returns version conflict
// errors for the first N update calls, then delegates to the inner repo.
type versionConflictMockRepo struct {
	inner           *MockRepository
	conflictsRemain int
	mu              sync.Mutex
}

func (r *versionConflictMockRepo) Create(ctx context.Context, po *domain.PaymentOrder) error {
	return r.inner.Create(ctx, po)
}

func (r *versionConflictMockRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.PaymentOrder, error) {
	return r.inner.FindByID(ctx, id)
}

func (r *versionConflictMockRepo) FindByIdempotencyKey(ctx context.Context, key string) (*domain.PaymentOrder, error) {
	return r.inner.FindByIdempotencyKey(ctx, key)
}

func (r *versionConflictMockRepo) FindByGatewayReferenceID(ctx context.Context, ref string) (*domain.PaymentOrder, error) {
	return r.inner.FindByGatewayReferenceID(ctx, ref)
}

func (r *versionConflictMockRepo) FindByDebtorAccountID(ctx context.Context, accountID string) ([]*domain.PaymentOrder, error) {
	return r.inner.FindByDebtorAccountID(ctx, accountID)
}

func (r *versionConflictMockRepo) FindByDebtorAccountIDWithCursor(ctx context.Context, accountID string, limit int, cursor persistence.Cursor) (*persistence.PaginatedResult, error) {
	return r.inner.FindByDebtorAccountIDWithCursor(ctx, accountID, limit, cursor)
}

func (r *versionConflictMockRepo) Update(ctx context.Context, po *domain.PaymentOrder) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conflictsRemain > 0 {
		r.conflictsRemain--
		return persistence.ErrPaymentOrderVersionConflict
	}
	return r.inner.Update(ctx, po)
}
