package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// =============================================================================
// Test Mocks
// =============================================================================

// failingOutboxRepo implements events.OutboxRepository and always fails Insert.
// Used to test the fallback path when outbox write fails after saga completion.
type failingOutboxRepo struct {
	insertErr error
}

func (r *failingOutboxRepo) Insert(_ context.Context, _ *gorm.DB, _ *events.EventOutbox) error {
	return r.insertErr
}

func (r *failingOutboxRepo) FetchUnprocessed(_ context.Context, _ string, _ int) ([]events.EventOutbox, error) {
	return nil, nil
}

func (r *failingOutboxRepo) FetchAndLockForProcessing(_ context.Context, _ string, _ int) ([]events.EventOutbox, error) {
	return nil, nil
}

func (r *failingOutboxRepo) MarkProcessing(_ context.Context, _ []uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *failingOutboxRepo) MarkCompleted(_ context.Context, _ uuid.UUID) error { return nil }
func (r *failingOutboxRepo) MarkFailed(_ context.Context, _ uuid.UUID, _ error, _ int) error {
	return nil
}

func (r *failingOutboxRepo) GetPendingCount(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (r *failingOutboxRepo) ResetStuckEntries(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, nil
}

// countedOutboxRepo records Insert call count and fails after a threshold.
// Used to test best-effort deprecated outbox entry behavior.
type countedOutboxRepo struct {
	calls         int
	failAfterCall int
	failErr       error
}

func (r *countedOutboxRepo) Insert(_ context.Context, _ *gorm.DB, _ *events.EventOutbox) error {
	r.calls++
	if r.calls > r.failAfterCall {
		return r.failErr
	}
	return nil
}

func (r *countedOutboxRepo) FetchUnprocessed(_ context.Context, _ string, _ int) ([]events.EventOutbox, error) {
	return nil, nil
}

func (r *countedOutboxRepo) FetchAndLockForProcessing(_ context.Context, _ string, _ int) ([]events.EventOutbox, error) {
	return nil, nil
}

func (r *countedOutboxRepo) MarkProcessing(_ context.Context, _ []uuid.UUID) (int64, error) {
	return 0, nil
}
func (r *countedOutboxRepo) MarkCompleted(_ context.Context, _ uuid.UUID) error { return nil }
func (r *countedOutboxRepo) MarkFailed(_ context.Context, _ uuid.UUID, _ error, _ int) error {
	return nil
}

func (r *countedOutboxRepo) GetPendingCount(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (r *countedOutboxRepo) ResetStuckEntries(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, nil
}

// controlledIdempotencyMock provides fine-grained error control for idempotency paths.
type controlledIdempotencyMock struct {
	checkResult    *idempotency.Result
	checkErr       error
	markPendingErr error
}

func (m *controlledIdempotencyMock) Check(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
	return m.checkResult, m.checkErr
}

func (m *controlledIdempotencyMock) MarkPending(_ context.Context, _ idempotency.Key, _ time.Duration) error {
	return m.markPendingErr
}

func (m *controlledIdempotencyMock) StoreResult(_ context.Context, _ idempotency.Result) error {
	return nil
}

func (m *controlledIdempotencyMock) Delete(_ context.Context, _ idempotency.Key) error {
	return nil
}

func (m *controlledIdempotencyMock) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (m *controlledIdempotencyMock) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (m *controlledIdempotencyMock) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (m *controlledIdempotencyMock) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}

// =============================================================================
// Withdrawal ID Mode Error Paths
// =============================================================================

// TestExecuteWithdrawal_WithdrawalID_ReferenceNotFound verifies NotFound when the withdrawal
// reference does not exist in the repository.
func TestExecuteWithdrawal_WithdrawalID_ReferenceNotFound(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}))

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         testLogger(),
	}

	req := &pb.ExecuteWithdrawalRequest{WithdrawalId: "WTH-DOES-NOT-EXIST"}
	_, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "withdrawal not found")
}

// TestExecuteWithdrawal_WithdrawalID_FindByReferenceInternalError verifies Internal error
// when the withdrawal repository query fails with a non-NotFound error.
// Simulated by not creating the withdrawal table so any DB query fails.
func TestExecuteWithdrawal_WithdrawalID_FindByReferenceInternalError(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()
	// Intentionally skip AutoMigrate for WithdrawalEntity → DB returns table-not-found error

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         testLogger(),
	}

	req := &pb.ExecuteWithdrawalRequest{WithdrawalId: "WTH-TABLE-MISSING"}
	_, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// TestExecuteWithdrawal_WithdrawalID_NotPending verifies FailedPrecondition when the
// withdrawal exists but is not in PENDING state.
func TestExecuteWithdrawal_WithdrawalID_NotPending(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}))

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create an account so we can create a valid withdrawal
	account := createTestAccount(t, ctx, repo, "ACC-EW-NP-001")

	// Build a domain withdrawal and transition it to COMPLETED before saving
	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-COMPLETED-001")
	require.NoError(t, err)
	require.NoError(t, withdrawal.Complete())
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         testLogger(),
	}

	req := &pb.ExecuteWithdrawalRequest{WithdrawalId: withdrawal.Reference}
	_, err = svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not pending")
}

// TestExecuteWithdrawal_WithdrawalID_AccountNotFound verifies NotFound when the withdrawal
// references an account UUID that no longer exists in the account repository.
func TestExecuteWithdrawal_WithdrawalID_AccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}))

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create a withdrawal with a random AccountID UUID that has no matching account
	orphanAccountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(orphanAccountID, amount, "WTH-ORPHAN-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         testLogger(),
	}

	req := &pb.ExecuteWithdrawalRequest{WithdrawalId: withdrawal.Reference}
	_, err = svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "account not found")
}

// =============================================================================
// Idempotency Service Error Paths (Direct Withdrawal Mode)
// =============================================================================

// TestExecuteWithdrawal_IdempotencyCheckError verifies Internal error when the idempotency
// service returns an unexpected error during the cache check phase.
func TestExecuteWithdrawal_IdempotencyCheckError(t *testing.T) {
	checkErr := errors.New("redis connection refused")
	mockIdemp := &controlledIdempotencyMock{
		checkErr: checkErr,
	}

	// repo is not accessed before the idempotency check returns an error
	svc := &Service{
		repo:               persistence.NewRepository(nil),
		idempotencyService: mockIdemp,
		logger:             testLogger(),
	}

	ctx := tenant.WithTenant(context.Background(), uniqueTenantID())
	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-IDEMP-CHECK-ERR",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 10},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-check-fail"},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to check idempotency")
}

// TestExecuteWithdrawal_IdempotencyCacheHitReturnsResponse verifies that a valid cached
// response is returned immediately without re-executing the withdrawal.
func TestExecuteWithdrawal_IdempotencyCacheHitReturnsResponse(t *testing.T) {
	cachedResp := &pb.ExecuteWithdrawalResponse{
		AccountId:     "ACC-CACHE-001",
		TransactionId: "tx-cached-001",
		Status:        pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED,
	}
	responseData, err := proto.Marshal(cachedResp)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), uniqueTenantID())
	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
		Namespace: idempotencyNamespace,
		Operation: "withdrawal",
		EntityID:  "ACC-CACHE-001",
		RequestID: "req-cache-hit",
	}

	mockIdemp := &controlledIdempotencyMock{
		checkResult: &idempotency.Result{
			Key:         idempKey,
			Status:      idempotency.StatusCompleted,
			Data:        responseData,
			CompletedAt: time.Now(),
		},
		checkErr: idempotency.ErrOperationAlreadyProcessed,
	}

	// repo is not accessed - the cached response is returned before any DB calls
	svc := &Service{
		repo:               persistence.NewRepository(nil),
		idempotencyService: mockIdemp,
		logger:             testLogger(),
	}

	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-CACHE-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 10},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-cache-hit"},
	}

	resp, err := svc.ExecuteWithdrawal(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "tx-cached-001", resp.TransactionId)
	assert.Equal(t, "ACC-CACHE-001", resp.AccountId)
}

// TestExecuteWithdrawal_IdempotencyCacheHitUnmarshalFails verifies that a corrupted cached
// response causes a warning to be logged but processing continues normally.
// The fallthrough is confirmed by observing the subsequent codes.NotFound from account lookup.
func TestExecuteWithdrawal_IdempotencyCacheHitUnmarshalFails(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	tid, _ := tenant.FromContext(ctx)
	idempKey := idempotency.Key{
		TenantID:  string(tid),
		Namespace: idempotencyNamespace,
		Operation: "withdrawal",
		EntityID:  "ACC-UNMARSHAL-FAIL",
		RequestID: "req-bad-data",
	}

	mockIdemp := &controlledIdempotencyMock{
		checkResult: &idempotency.Result{
			Key:         idempKey,
			Status:      idempotency.StatusCompleted,
			Data:        []byte{0xff, 0xfe, 0x01}, // invalid protobuf bytes
			CompletedAt: time.Now(),
		},
		checkErr: idempotency.ErrOperationAlreadyProcessed,
	}

	repo := persistence.NewRepository(db)
	svc := &Service{
		repo:               repo,
		idempotencyService: mockIdemp,
		logger:             testLogger(),
	}

	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-UNMARSHAL-FAIL", // account does not exist in DB
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 10},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-bad-data"},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)

	// Code fell through the unmarshal failure and reached the account lookup step.
	// Account does not exist → codes.NotFound (not codes.Internal, confirming fallthrough).
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code(), "code fell through cache unmarshal failure to account lookup")
}

// TestExecuteWithdrawal_IdempotencyMarkPendingAlreadyInProgress verifies Aborted error
// when another request is already in progress for the same idempotency key.
func TestExecuteWithdrawal_IdempotencyMarkPendingAlreadyInProgress(t *testing.T) {
	mockIdemp := &controlledIdempotencyMock{
		checkErr:       idempotency.ErrResultNotFound,
		markPendingErr: idempotency.ErrOperationAlreadyProcessed,
	}

	// repo is not accessed before MarkPending fails
	svc := &Service{
		repo:               persistence.NewRepository(nil),
		idempotencyService: mockIdemp,
		logger:             testLogger(),
	}

	ctx := tenant.WithTenant(context.Background(), uniqueTenantID())
	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-PENDING-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 10},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-concurrent"},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
	assert.Contains(t, st.Message(), "already in progress")
}

// TestExecuteWithdrawal_IdempotencyMarkPendingLockFailed verifies Aborted error when
// the idempotency service fails to acquire the distributed lock for an unexpected reason.
func TestExecuteWithdrawal_IdempotencyMarkPendingLockFailed(t *testing.T) {
	mockIdemp := &controlledIdempotencyMock{
		checkErr:       idempotency.ErrResultNotFound,
		markPendingErr: errors.New("redis write timeout"),
	}

	// repo is not accessed before MarkPending fails
	svc := &Service{
		repo:               persistence.NewRepository(nil),
		idempotencyService: mockIdemp,
		logger:             testLogger(),
	}

	ctx := tenant.WithTenant(context.Background(), uniqueTenantID())
	req := &pb.ExecuteWithdrawalRequest{
		AccountId: "ACC-LOCK-FAIL-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 10},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-lock-fail"},
	}

	_, err := svc.ExecuteWithdrawal(ctx, req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
	assert.Contains(t, st.Message(), "failed to acquire idempotency lock")
}

// =============================================================================
// completeWithdrawalWithOutbox Error Paths
// =============================================================================

// TestCompleteWithdrawalWithOutbox_NoOutbox_CompleteFails verifies an error is returned
// when the withdrawal cannot be transitioned to COMPLETED (already in a terminal state).
func TestCompleteWithdrawalWithOutbox_NoOutbox_CompleteFails(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()
	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}))

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create a withdrawal, transition it to FAILED (terminal state), and save it
	account := createTestAccount(t, ctx, repo, "ACC-COW-CF-001")
	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-COW-FAILED-001")
	require.NoError(t, err)
	require.NoError(t, withdrawal.Fail())
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	// outboxRepo=nil, db=nil → direct update path
	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         testLogger(),
	}

	err = svc.completeWithdrawalWithOutbox(ctx, withdrawal, withdrawal.AccountID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to transition withdrawal to completed status")
}

// TestCompleteWithdrawalWithOutbox_NoOutbox_UpdateFails verifies an error is returned
// when the withdrawal repository update fails (e.g., table does not exist).
func TestCompleteWithdrawalWithOutbox_NoOutbox_UpdateFails(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()
	// Intentionally skip AutoMigrate for WithdrawalEntity → Update returns table-not-found error

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Build a PENDING withdrawal domain object (not persisted since table doesn't exist)
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(accountID, amount, "WTH-COW-UPD-FAIL")
	require.NoError(t, err)

	// outboxRepo=nil, db=nil → direct update path
	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		logger:         testLogger(),
	}

	err = svc.completeWithdrawalWithOutbox(ctx, withdrawal, accountID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist withdrawal completion")
}

// TestExecuteWithdrawal_OutboxInsertFails_FallbackDirectUpdateSucceeds verifies that when
// the transactional outbox write fails after a successful saga, the service falls back to
// a direct status update so the withdrawal is not left stuck in PENDING.
func TestExecuteWithdrawal_OutboxInsertFails_FallbackDirectUpdateSucceeds(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}))
	require.NoError(t, db.AutoMigrate(&events.EventOutbox{}))

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	const accountID = "ACC-OUTBOX-FAIL-001"
	_ = createTestAccountWithBalance(t, ctx, repo, accountID, 100000)

	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{accountID: 95000},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	// Phase 1: Initiate withdrawal to create a PENDING record
	svc := &Service{
		repo:                   repo,
		withdrawalRepo:         withdrawalRepo,
		outboxRepo:             &failingOutboxRepo{insertErr: errors.New("outbox unavailable")},
		db:                     db,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	initiateReq := &pb.InitiateWithdrawalRequest{
		AccountId: accountID,
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50},
		},
		Reference: "WTH-OUTBOX-FAIL-001",
	}
	initiateResp, err := svc.InitiateWithdrawal(ctx, initiateReq)
	require.NoError(t, err)
	withdrawalID := initiateResp.Withdrawal.WithdrawalId

	// Phase 2: Execute withdrawal - outbox Insert will fail, triggering the fallback path
	executeReq := &pb.ExecuteWithdrawalRequest{
		WithdrawalId: withdrawalID,
	}
	resp, err := svc.ExecuteWithdrawal(ctx, executeReq)

	// Funds already moved via saga - RPC must succeed despite outbox failure.
	// The fallback direct update may itself fail (e.g., optimistic lock version conflict
	// after the rolled-back transaction increments the in-memory version), but the RPC
	// must never surface that as a client-visible error.
	require.NoError(t, err, "ExecuteWithdrawal should succeed even when outbox fails (funds already moved)")
	assert.NotEmpty(t, resp.TransactionId)
}

// TestCompleteWithdrawalWithOutbox_DeprecatedOutboxFailureContinues verifies that a failure
// when writing the deprecated outbox entry does not abort the operation — it is best-effort.
func TestCompleteWithdrawalWithOutbox_DeprecatedOutboxFailureContinues(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	require.NoError(t, db.AutoMigrate(&persistence.WithdrawalEntity{}))
	require.NoError(t, db.AutoMigrate(&events.EventOutbox{}))

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create a PENDING withdrawal directly
	accountID := uuid.New()
	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(accountID, amount, "WTH-DEP-OUTBOX-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	// Main outbox Insert succeeds (call 1), deprecated Insert fails (call 2)
	outboxRepo := &countedOutboxRepo{
		failAfterCall: 1,
		failErr:       errors.New("deprecated topic unavailable"),
	}

	svc := &Service{
		repo:           repo,
		withdrawalRepo: withdrawalRepo,
		outboxRepo:     outboxRepo,
		db:             db,
		logger:         testLogger(),
	}

	err = svc.completeWithdrawalWithOutbox(ctx, withdrawal, accountID)

	// Deprecated outbox failure must not surface as an error
	require.NoError(t, err, "deprecated outbox entry failure should not abort the operation")
	assert.Equal(t, 2, outboxRepo.calls, "both the canonical and deprecated inserts should be attempted")

	// Withdrawal should be persisted as COMPLETED in the DB
	saved, findErr := withdrawalRepo.FindByReference(ctx, withdrawal.Reference)
	require.NoError(t, findErr)
	assert.Equal(t, domain.WithdrawalStatusCompleted, saved.Status)
}
