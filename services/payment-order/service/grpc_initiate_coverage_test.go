package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// initiateIdempKey reconstructs the idempotency key that
// validateInitiateIdempotencyKey builds for an InitiatePaymentOrder request,
// so tests can pre-seed the mock idempotency store with cached results.
func initiateIdempKey(debtorAccountID, requestID string) idempotency.Key {
	return idempotency.Key{
		TenantID:  "",
		Namespace: idempotencyNamespace,
		Operation: "initiate",
		EntityID:  debtorAccountID,
		RequestID: requestID,
	}
}

// TestInitiatePaymentOrder_KeyExceedsMaxLength covers the maximum-length
// validation branch in validateInitiateIdempotencyKey.
func TestInitiatePaymentOrder_KeyExceedsMaxLength(t *testing.T) {
	repo := NewMockRepository()
	svc, err := NewService(repo, NewMockIdempotencyService())
	require.NoError(t, err)

	longKey := make([]byte, svc.maxIdempotencyKeyLength+1)
	for i := range longKey {
		longKey[i] = 'a'
	}

	req := newInitiateRequest(string(longKey), "ACC-12345678", "GB82WEST12345698765432", 10000)

	_, err = svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "maximum length")
}

// TestInitiatePaymentOrder_RedisCacheHit covers the Redis cached-result branch
// where a previously completed operation returns its cached response without
// touching the repository.
func TestInitiatePaymentOrder_RedisCacheHit(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	cachedID := uuid.New().String()
	cached := &pb.InitiatePaymentOrderResponse{
		PaymentOrder: &pb.PaymentOrder{
			PaymentOrderId:  cachedID,
			DebtorAccountId: "ACC-12345678",
		},
	}
	data, marshalErr := proto.Marshal(cached)
	require.NoError(t, marshalErr)

	key := initiateIdempKey("ACC-12345678", "cache-hit-key")
	require.NoError(t, idempSvc.StoreResult(context.Background(), idempotency.Result{
		Key:    key,
		Status: idempotency.StatusCompleted,
		Data:   data,
	}))

	req := newInitiateRequest("cache-hit-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	resp, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, cachedID, resp.PaymentOrder.PaymentOrderId)
	// The cached path must not create a new payment order in the repository.
	_, findErr := repo.FindByIdempotencyKey(context.Background(), "cache-hit-key")
	assert.ErrorIs(t, findErr, persistence.ErrPaymentOrderNotFound)
}

// TestInitiatePaymentOrder_RedisCacheCorrupt covers the branch where a cached
// result exists but its payload cannot be unmarshaled, so the service falls
// back to normal processing and creates the payment order.
func TestInitiatePaymentOrder_RedisCacheCorrupt(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	key := initiateIdempKey("ACC-12345678", "corrupt-key")
	require.NoError(t, idempSvc.StoreResult(context.Background(), idempotency.Result{
		Key:    key,
		Status: idempotency.StatusCompleted,
		Data:   []byte{0xff, 0xff, 0xff, 0xff}, // not a valid proto message
	}))

	req := newInitiateRequest("corrupt-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	resp, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.NotEmpty(t, resp.PaymentOrder.PaymentOrderId)
	// Falling back to normal processing must persist a new payment order.
	stored, findErr := repo.FindByIdempotencyKey(context.Background(), "corrupt-key")
	require.NoError(t, findErr)
	assert.Equal(t, resp.PaymentOrder.PaymentOrderId, stored.ID.String())
}

// TestInitiatePaymentOrder_RedisCheckError covers the branch where the
// idempotency Check call fails with an unexpected (non-not-found) error.
func TestInitiatePaymentOrder_RedisCheckError(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	idempSvc.checkErr = errGatewayUnavailable // arbitrary non-not-found error
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	req := newInitiateRequest("check-err-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	_, err = svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "check idempotency")
}

// TestInitiatePaymentOrder_MarkPendingError covers the branch where acquiring
// the distributed idempotency lock fails.
func TestInitiatePaymentOrder_MarkPendingError(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	idempSvc.markPendErr = errGatewayUnavailable
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	req := newInitiateRequest("markpend-err-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	_, err = svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "idempotency lock")
}

// TestInitiatePaymentOrder_DatabaseIdempotencyHit covers the database-fallback
// idempotency branch where an existing payment order with the same key is
// returned. The Redis store is pre-seeded with a pending (not completed) record
// so the Redis cache path is skipped and the database check runs.
func TestInitiatePaymentOrder_DatabaseIdempotencyHit(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	amount, _ := domain.NewMoney("GBP", 10000)
	existing, err := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "db-hit-key", uuid.New().String())
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), existing))

	req := newInitiateRequest("db-hit-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	resp, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, existing.ID.String(), resp.PaymentOrder.PaymentOrderId)
}

// TestInitiatePaymentOrder_DatabaseIdempotencyError covers the branch where the
// database idempotency lookup fails with an unexpected error.
func TestInitiatePaymentOrder_DatabaseIdempotencyError(t *testing.T) {
	repo := NewMockRepository()
	repo.findByIdempotencyErr = ErrDatabaseError
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	req := newInitiateRequest("db-err-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	_, err = svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "check idempotency")
}

// TestInitiatePaymentOrder_DomainValidationError covers the branch where
// domain.NewPaymentOrder rejects the request (here: missing creditor
// reference) after amount validation passes, and a failure result is stored.
func TestInitiatePaymentOrder_DomainValidationError(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	req := &pb.InitiatePaymentOrderRequest{
		DebtorAccountId:   "ACC-12345678",
		CreditorReference: "", // triggers domain.ErrMissingCreditorReference
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "domain-err-key"},
	}

	_, err = svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// A failure result should have been recorded for idempotency tracking.
	key := initiateIdempKey("ACC-12345678", "domain-err-key")
	result, checkErr := idempSvc.Check(context.Background(), key)
	require.NoError(t, checkErr)
	require.NotNil(t, result)
	assert.Equal(t, idempotency.StatusFailed, result.Status)
}

// TestInitiatePaymentOrder_InvalidAmountStoresFailure covers the invalid-amount
// (proto conversion) branch which also records an idempotency failure result.
func TestInitiatePaymentOrder_InvalidAmountStoresFailure(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	// Nil Amount triggers protoToMoney failure (invalid amount), distinct from
	// the "amount must be positive" branch.
	req := &pb.InitiatePaymentOrderRequest{
		DebtorAccountId:   "ACC-12345678",
		CreditorReference: "GB82WEST12345698765432",
		Amount:            nil,
		IdempotencyKey:    &commonpb.IdempotencyKey{Key: "invalid-amount-key"},
	}

	_, err = svc.InitiatePaymentOrder(context.Background(), req)

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	key := initiateIdempKey("ACC-12345678", "invalid-amount-key")
	result, checkErr := idempSvc.Check(context.Background(), key)
	require.NoError(t, checkErr)
	require.NotNil(t, result)
	assert.Equal(t, idempotency.StatusFailed, result.Status)
}

// TestHandleCreateConflict_ResolvesToExistingOrder covers the TOCTOU
// idempotency-conflict path where Create fails with a conflict and the existing
// order is reloaded and returned.
func TestHandleCreateConflict_ResolvesToExistingOrder(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	amount, _ := domain.NewMoney("GBP", 10000)
	existing, err := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "conflict-key", uuid.New().String())
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), existing))

	po, err := svc.handleCreateConflict(context.Background(), persistence.ErrIdempotencyKeyConflict, "conflict-key")

	require.NoError(t, err)
	require.NotNil(t, po)
	assert.Equal(t, existing.ID, po.ID)
}

// TestHandleCreateConflict_ReloadFails covers the conflict path where reloading
// the existing order after the conflict fails.
func TestHandleCreateConflict_ReloadFails(t *testing.T) {
	repo := NewMockRepository()
	repo.findByIdempotencyErr = ErrDatabaseError
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	po, err := svc.handleCreateConflict(context.Background(), persistence.ErrIdempotencyKeyConflict, "missing-key")

	require.Error(t, err)
	assert.Nil(t, po)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "retrieve payment order")
}

// TestHandleCreateConflict_GenericError covers the non-conflict Create failure
// path which surfaces a generic internal error.
func TestHandleCreateConflict_GenericError(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	po, err := svc.handleCreateConflict(context.Background(), ErrDatabaseError, "any-key")

	require.Error(t, err)
	assert.Nil(t, po)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "save payment order")
}

// TestInitiatePaymentOrder_CreateConflictRace exercises the full
// InitiatePaymentOrder flow when Create fails with an idempotency conflict,
// covering the !isNew early return that avoids publishing duplicate events.
func TestInitiatePaymentOrder_CreateConflictRace(t *testing.T) {
	repo := NewMockRepository()
	repo.createErr = persistence.ErrIdempotencyKeyConflict
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	// Seed an existing order so the post-conflict reload succeeds.
	amount, _ := domain.NewMoney("GBP", 10000)
	existing, err := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "race-key", uuid.New().String())
	require.NoError(t, err)
	// Insert directly into the idempotency index, bypassing the injected
	// createErr, to simulate the winner of the race already being persisted.
	repo.idempotencyKeyIndex["race-key"] = existing
	repo.paymentOrders[existing.ID] = existing

	req := newInitiateRequest("race-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	resp, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, existing.ID.String(), resp.PaymentOrder.PaymentOrderId)
}

// TestInitiatePaymentOrder_StoreResultErrorStillSucceeds covers the branch in
// publishAndCacheInitiateResult where caching the successful result fails but
// the request still returns the created order.
func TestInitiatePaymentOrder_StoreResultErrorStillSucceeds(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	// MarkPending uses markPendErr; StoreResult uses storeErr. Setting storeErr
	// alone lets the lock be acquired while the final result cache write fails.
	idempSvc.storeErr = ErrDatabaseError

	req := newInitiateRequest("store-err-key", "ACC-12345678", "GB82WEST12345698765432", 10000)
	resp, err := svc.InitiatePaymentOrder(context.Background(), req)

	require.NoError(t, err)
	assert.NotEmpty(t, resp.PaymentOrder.PaymentOrderId)
}

// TestHandleSagaPanic_MarksOrderFailed directly covers handleSagaPanic, which
// reloads the order and transitions it to FAILED. This path is reached in
// production only when the saga orchestration goroutine panics; the orchestrator
// is a concrete dependency that cannot be made to panic via the public API, so
// the method is exercised directly.
func TestHandleSagaPanic_MarksOrderFailed(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	amount, _ := domain.NewMoney("GBP", 10000)
	po, err := domain.NewPaymentOrder("ACC-12345678", "GB82WEST12345698765432", amount, "panic-key", uuid.New().String())
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), po))

	svc.handleSagaPanic(po.ID, "", false)

	err = await.New().Until(func() bool {
		reloaded, findErr := repo.FindByID(context.Background(), po.ID)
		return findErr == nil && reloaded.Status == domain.PaymentOrderStatusFailed
	})
	require.NoError(t, err)
}

// TestHandleSagaPanic_ReloadFails covers the handleSagaPanic branch where the
// order cannot be reloaded (e.g., not found), which logs and returns without
// panicking.
func TestHandleSagaPanic_ReloadFails(t *testing.T) {
	repo := NewMockRepository()
	idempSvc := NewMockIdempotencyService()
	svc, err := NewService(repo, idempSvc)
	require.NoError(t, err)

	// No order persisted, so FindByID returns ErrPaymentOrderNotFound.
	assert.NotPanics(t, func() {
		svc.handleSagaPanic(uuid.New(), "", false)
	})
}
