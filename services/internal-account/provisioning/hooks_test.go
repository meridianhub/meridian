package provisioning

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test sentinel error for simulated failures.
var errSimulatedFailure = errors.New("simulated failure")

func TestCreateDefaultAccountHook_Success(t *testing.T) {
	mock := newMockService()
	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_test_bank")

	hook := CreateDefaultAccountHook(provisioner, nil)

	err := hook(context.Background(), tenantID)

	require.NoError(t, err)
	assert.Equal(t, 11, len(mock.createdAccounts), "should create all default accounts")
}

func TestCreateDefaultAccountHook_Idempotent(t *testing.T) {
	mock := newMockService()
	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_test_bank")

	hook := CreateDefaultAccountHook(provisioner, nil)

	// First call creates accounts
	err := hook(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, 11, len(mock.createdAccounts))

	// Second call skips existing accounts
	err = hook(context.Background(), tenantID)
	require.NoError(t, err)
	// Should still be 11 since duplicates are skipped
	assert.Equal(t, 11, len(mock.createdAccounts))
}

func TestCreateDefaultAccountHook_PartialFailure(t *testing.T) {
	mock := newMockService()
	// Simulate failure for one account
	mock.failOnCodes["REV-TRANSACTION-FEE"] = errSimulatedFailure

	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_test_bank")

	hook := CreateDefaultAccountHook(provisioner, nil)

	err := hook(context.Background(), tenantID)

	// Hook returns error on partial failure (logged by worker but non-blocking)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REV-TRANSACTION-FEE")
}

func TestCreateDefaultAccountHook_ContextCancellation(t *testing.T) {
	mock := &slowMockService{
		delay: DefaultAccountHookTimeout * 2, // Longer than timeout
	}
	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_test_bank")

	hook := CreateDefaultAccountHook(provisioner, nil)

	// Hook has internal timeout of 30 seconds - use shorter test timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := hook(ctx, tenantID)

	// Should return error due to timeout
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// slowMockService simulates a slow service for timeout testing.
type slowMockService struct {
	delay time.Duration
}

func (m *slowMockService) InitiateInternalAccount(ctx context.Context, req *pb.InitiateInternalAccountRequest) (*pb.InitiateInternalAccountResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(m.delay):
		return &pb.InitiateInternalAccountResponse{
			AccountId: "IBA-test-" + req.AccountCode,
		}, nil
	}
}
