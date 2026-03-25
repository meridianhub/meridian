package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// ExecuteLien – MarkPending returns arbitrary error
// ---------------------------------------------------------------------------

// TestExecuteLien_MarkPendingFailed_ArbitraryError verifies that when MarkPending
// returns a non-ErrOperationAlreadyProcessed error, the service returns Aborted
// with a "failed to acquire idempotency lock" message.
func TestExecuteLien_MarkPendingFailed_ArbitraryError(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	mockIdemp.markPendingErr = assert.AnError

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil),
	)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test-tenant"))
	lienID := uuid.New()

	req := &pb.ExecuteLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "exec-mark-fail-001"},
	}

	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
	assert.Contains(t, err.Error(), "failed to acquire idempotency lock")
}

// ---------------------------------------------------------------------------
// TerminateLien – MarkPending returns arbitrary error
// ---------------------------------------------------------------------------

// TestTerminateLien_MarkPendingFailed_ArbitraryError verifies that when MarkPending
// returns a non-ErrOperationAlreadyProcessed error, the service returns Aborted.
func TestTerminateLien_MarkPendingFailed_ArbitraryError(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()
	mockIdemp.markPendingErr = assert.AnError

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil),
	)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test-tenant"))
	lienID := uuid.New()

	req := &pb.TerminateLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "term-mark-fail-001"},
	}

	_, err = svc.TerminateLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))
	assert.Contains(t, err.Error(), "failed to acquire idempotency lock")
}

// ---------------------------------------------------------------------------
// ExecuteLien – cached result present but unmarshal fails (falls through)
// ---------------------------------------------------------------------------

// TestExecuteLien_CachedResultUnmarshalFails verifies that when a cached
// idempotency result is present but the data cannot be unmarshalled, the
// request falls through to the normal execution path.
func TestExecuteLien_CachedResultUnmarshalFails(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil), // nil lienRepo → FailedPrecondition after fallthrough
	)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test-tenant"))
	lienID := uuid.New()

	idempKey := idempotency.Key{
		TenantID:  "test-tenant",
		Namespace: idempotencyNamespace,
		Operation: "execute_lien",
		EntityID:  lienID.String(),
		RequestID: "exec-corrupt-001",
	}
	// Store an invalid proto payload so unmarshal will fail
	mockIdemp.setResult(idempKey, []byte("not-valid-proto-data"))

	req := &pb.ExecuteLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "exec-corrupt-001"},
	}

	// Unmarshal fails → falls through → MarkPending succeeds → nil lienRepo → FailedPrecondition
	_, err = svc.ExecuteLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// TerminateLien – cached result present but unmarshal fails (falls through)
// ---------------------------------------------------------------------------

// TestTerminateLien_CachedResultUnmarshalFails verifies that when a cached
// termination result cannot be unmarshalled, the request falls through to
// the normal execution path.
func TestTerminateLien_CachedResultUnmarshalFails(t *testing.T) {
	repo := newMockRepository()
	mockIdemp := newMockIdempotencyService()

	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil,
		WithIdempotencyService(mockIdemp),
		WithLienRepo(nil), // nil lienRepo → FailedPrecondition after fallthrough
	)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test-tenant"))
	lienID := uuid.New()

	idempKey := idempotency.Key{
		TenantID:  "test-tenant",
		Namespace: idempotencyNamespace,
		Operation: "terminate_lien",
		EntityID:  lienID.String(),
		RequestID: "term-corrupt-001",
	}
	// Store an invalid proto payload so unmarshal will fail
	mockIdemp.setResult(idempKey, []byte("not-valid-proto-data"))

	req := &pb.TerminateLienRequest{
		LienId:         lienID.String(),
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "term-corrupt-001"},
	}

	// Unmarshal fails → falls through → MarkPending succeeds → nil lienRepo → FailedPrecondition
	_, err = svc.TerminateLien(ctx, req)
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}
