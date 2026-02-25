package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// mockIdempotencyService implements idempotency.Service for unit testing.
type mockIdempotencyService struct {
	mu        sync.Mutex
	results   map[string]*idempotency.Result
	pending   map[string]bool
	checkErr  error
	storeErr  error
	deleteErr error
}

func newMockIdempotencyService() *mockIdempotencyService {
	return &mockIdempotencyService{
		results: make(map[string]*idempotency.Result),
		pending: make(map[string]bool),
	}
}

func (m *mockIdempotencyService) Check(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.checkErr != nil {
		return nil, m.checkErr
	}

	keyStr := key.String()
	if result, ok := m.results[keyStr]; ok {
		if result.Status == idempotency.StatusCompleted {
			return result, idempotency.ErrOperationAlreadyProcessed
		}
		return result, nil
	}
	return nil, idempotency.ErrResultNotFound
}

func (m *mockIdempotencyService) MarkPending(_ context.Context, key idempotency.Key, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	keyStr := key.String()
	if m.pending[keyStr] {
		return idempotency.ErrOperationAlreadyProcessed
	}
	m.pending[keyStr] = true
	return nil
}

func (m *mockIdempotencyService) StoreResult(_ context.Context, result idempotency.Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.storeErr != nil {
		return m.storeErr
	}

	keyStr := result.Key.String()
	m.results[keyStr] = &result
	delete(m.pending, keyStr)
	return nil
}

func (m *mockIdempotencyService) Delete(_ context.Context, key idempotency.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	keyStr := key.String()
	delete(m.results, keyStr)
	delete(m.pending, keyStr)
	return nil
}

func (m *mockIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (m *mockIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (m *mockIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (m *mockIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}

func (m *mockIdempotencyService) setResult(key idempotency.Key, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[key.String()] = &idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        data,
		CompletedAt: time.Now(),
	}
}

func (m *mockIdempotencyService) setPending(key idempotency.Key) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[key.String()] = true
}

// TestWithIdempotencyService_WiresField verifies the functional option sets the field.
func TestWithIdempotencyService_WiresField(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)
	assert.NotNil(t, svc.idempotencyService, "idempotency service should be wired")
}

// TestWithIdempotencyService_NilDoesNotOverride verifies a nil option is accepted without panic.
func TestWithIdempotencyService_NilDoesNotOverride(t *testing.T) {
	repo := newMockRepository()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(nil),
	)
	require.NoError(t, err)
	assert.Nil(t, svc.idempotencyService)
}

// TestExecuteLien_IdempotencyReturnsCachedResponse verifies that a prior cached Redis result
// is returned without hitting the database.
func TestExecuteLien_IdempotencyReturnsCachedResponse(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	const tenantID = "test-tenant"
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
	lienID := uuid.New()

	// Pre-populate cached response
	idempKey := idempotency.Key{
		TenantID:  tenantID,
		Namespace: idempotencyNamespace,
		Operation: "execute_lien",
		EntityID:  lienID.String(),
		RequestID: "exec-req-abc",
	}
	cachedResp := &pb.ExecuteLienResponse{
		Lien: &pb.Lien{
			LienId: lienID.String(),
			Status: pb.LienStatus_LIEN_STATUS_EXECUTED,
		},
	}
	cachedData, err := proto.Marshal(cachedResp)
	require.NoError(t, err)
	mockIdemp.setResult(idempKey, cachedData)

	// Execute with same key — should return cached result without DB access
	req := &pb.ExecuteLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "exec-req-abc"},
	}

	resp, err := svc.ExecuteLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_EXECUTED, resp.Lien.Status)
}

// TestExecuteLien_IdempotencyReturnsAbortedWhenInProgress verifies that a concurrent request
// in-progress returns codes.Aborted.
func TestExecuteLien_IdempotencyReturnsAbortedWhenInProgress(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	const tenantID = "test-tenant"
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
	lienID := uuid.New()

	// Mark as already pending
	idempKey := idempotency.Key{
		TenantID:  tenantID,
		Namespace: idempotencyNamespace,
		Operation: "execute_lien",
		EntityID:  lienID.String(),
		RequestID: "exec-req-in-progress",
	}
	mockIdemp.setPending(idempKey)

	req := &pb.ExecuteLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "exec-req-in-progress"},
	}

	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
}

// TestExecuteLien_NoIdempotencyKey_ProceedsNormally verifies that omitting the idempotency key
// bypasses the Redis guard entirely (no nil panic, proceeds to lien repo lookup).
func TestExecuteLien_NoIdempotencyKey_ProceedsNormally(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil), // nil lien repo → immediate FailedPrecondition
	)
	require.NoError(t, err)

	ctx := context.Background()
	lienID := uuid.New()

	// No idempotency key → bypasses guard → hits lienRepo nil check
	req := &pb.ExecuteLienRequest{
		LienId: lienID.String(),
		// No IdempotencyKey
	}

	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// TestExecuteLien_IdempotencyCheckFailure verifies that a Redis check error returns Internal.
func TestExecuteLien_IdempotencyCheckFailure(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkErr = assert.AnError

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil),
	)
	require.NoError(t, err)

	const tenantID = "test-tenant"
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
	lienID := uuid.New()

	req := &pb.ExecuteLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "exec-req-err"},
	}

	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// TestTerminateLien_IdempotencyReturnsCachedResponse verifies cached termination response
// is returned without hitting the database.
func TestTerminateLien_IdempotencyReturnsCachedResponse(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	const tenantID = "test-tenant"
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
	lienID := uuid.New()

	// Pre-populate cached response
	idempKey := idempotency.Key{
		TenantID:  tenantID,
		Namespace: idempotencyNamespace,
		Operation: "terminate_lien",
		EntityID:  lienID.String(),
		RequestID: "term-req-xyz",
	}
	cachedResp := &pb.TerminateLienResponse{
		Lien: &pb.Lien{
			LienId: lienID.String(),
			Status: pb.LienStatus_LIEN_STATUS_TERMINATED,
		},
	}
	cachedData, err := proto.Marshal(cachedResp)
	require.NoError(t, err)
	mockIdemp.setResult(idempKey, cachedData)

	req := &pb.TerminateLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "term-req-xyz"},
	}

	resp, err := svc.TerminateLien(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, resp.Lien.Status)
}

// TestTerminateLien_IdempotencyReturnsAbortedWhenInProgress verifies concurrent termination
// returns codes.Aborted.
func TestTerminateLien_IdempotencyReturnsAbortedWhenInProgress(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
	)
	require.NoError(t, err)

	const tenantID = "test-tenant"
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
	lienID := uuid.New()

	// Mark as already pending
	idempKey := idempotency.Key{
		TenantID:  tenantID,
		Namespace: idempotencyNamespace,
		Operation: "terminate_lien",
		EntityID:  lienID.String(),
		RequestID: "term-req-in-progress",
	}
	mockIdemp.setPending(idempKey)

	req := &pb.TerminateLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "term-req-in-progress"},
	}

	_, err = svc.TerminateLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
}

// TestTerminateLien_NoIdempotencyKey_ProceedsNormally verifies that omitting the idempotency key
// bypasses the Redis guard.
func TestTerminateLien_NoIdempotencyKey_ProceedsNormally(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil), // nil lien repo → immediate FailedPrecondition
	)
	require.NoError(t, err)

	ctx := context.Background()
	lienID := uuid.New()

	req := &pb.TerminateLienRequest{
		LienId: lienID.String(),
		// No IdempotencyKey
	}

	_, err = svc.TerminateLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// TestTerminateLien_IdempotencyCheckFailure verifies a Redis error returns Internal.
func TestTerminateLien_IdempotencyCheckFailure(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	mockIdemp.checkErr = assert.AnError

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil),
	)
	require.NoError(t, err)

	const tenantID = "test-tenant"
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
	lienID := uuid.New()

	req := &pb.TerminateLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "term-req-err"},
	}

	_, err = svc.TerminateLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// TestIdempotencyConstants verifies the namespace, pending TTL, and result TTL are set correctly.
func TestIdempotencyConstants(t *testing.T) {
	assert.Equal(t, "internal-account", idempotencyNamespace)
	assert.Equal(t, 5*time.Minute, idempotencyPendingTTL)
	assert.Equal(t, 24*time.Hour, idempotencyResultTTL)
}
