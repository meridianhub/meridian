package stripe

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTenantVerifier struct {
	tenants map[string]bool
	err     error
}

func (m *mockTenantVerifier) TenantExists(_ context.Context, tenantID string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.tenants[tenantID], nil
}

type mockAccountVerifier struct {
	accounts map[string]bool
	err      error
}

func (m *mockAccountVerifier) AccountExists(_ context.Context, tenantID, accountID string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	key := tenantID + "/" + accountID
	return m.accounts[key], nil
}

func TestPreflightCheck_AllPassing(t *testing.T) {
	checker := NewPreflightChecker(PreflightConfig{
		TenantVerifier: &mockTenantVerifier{
			tenants: map[string]bool{"meridian-ops": true},
		},
		AccountVerifier: &mockAccountVerifier{
			accounts: map[string]bool{
				"meridian-ops/stripe_nostro": true,
				"meridian-ops/revenue":       true,
			},
		},
	})

	err := checker.Check(context.Background())
	assert.NoError(t, err)
}

func TestPreflightCheck_TenantMissing(t *testing.T) {
	checker := NewPreflightChecker(PreflightConfig{
		TenantVerifier: &mockTenantVerifier{
			tenants: map[string]bool{},
		},
	})

	err := checker.Check(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPreflightFailed)
	assert.ErrorIs(t, err, ErrTenantNotFound)
}

func TestPreflightCheck_NostroMissing(t *testing.T) {
	checker := NewPreflightChecker(PreflightConfig{
		TenantVerifier: &mockTenantVerifier{
			tenants: map[string]bool{"meridian-ops": true},
		},
		AccountVerifier: &mockAccountVerifier{
			accounts: map[string]bool{
				"meridian-ops/revenue": true,
			},
		},
	})

	err := checker.Check(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPreflightFailed)
	assert.ErrorIs(t, err, ErrNostroNotFound)
}

func TestPreflightCheck_RevenueMissing(t *testing.T) {
	checker := NewPreflightChecker(PreflightConfig{
		TenantVerifier: &mockTenantVerifier{
			tenants: map[string]bool{"meridian-ops": true},
		},
		AccountVerifier: &mockAccountVerifier{
			accounts: map[string]bool{
				"meridian-ops/stripe_nostro": true,
			},
		},
	})

	err := checker.Check(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPreflightFailed)
	assert.ErrorIs(t, err, ErrRevenueNotFound)
}

func TestPreflightCheck_TenantVerifierError(t *testing.T) {
	checker := NewPreflightChecker(PreflightConfig{
		TenantVerifier: &mockTenantVerifier{
			err: errors.New("connection refused"),
		},
	})

	err := checker.Check(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPreflightFailed)
}

func TestPreflightCheck_NilVerifiers(t *testing.T) {
	// With nil verifiers, check should pass (graceful degradation)
	checker := NewPreflightChecker(PreflightConfig{})

	err := checker.Check(context.Background())
	assert.NoError(t, err)
}

func TestPreflightCheck_AccountVerifierError(t *testing.T) {
	checker := NewPreflightChecker(PreflightConfig{
		TenantVerifier: &mockTenantVerifier{
			tenants: map[string]bool{"meridian-ops": true},
		},
		AccountVerifier: &mockAccountVerifier{
			err: errors.New("account service unavailable"),
		},
	})

	err := checker.Check(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPreflightFailed)
	assert.Contains(t, err.Error(), "account service unavailable")
}

func TestPreflightCheck_RevenueAccountVerifierError(t *testing.T) {
	// Nostro account exists but revenue check fails
	checker := NewPreflightChecker(PreflightConfig{
		TenantVerifier: &mockTenantVerifier{
			tenants: map[string]bool{"meridian-ops": true},
		},
		AccountVerifier: &mockAccountVerifier{
			accounts: map[string]bool{
				"meridian-ops/stripe_nostro": true,
				// revenue not present, so it returns false, not an error
			},
		},
	})

	err := checker.Check(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPreflightFailed)
	assert.ErrorIs(t, err, ErrRevenueNotFound)
}
