package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTenantID = tenant.MustNewTenantID("test_tenant")

func TestNewIdentity_ValidEmail(t *testing.T) {
	emails := []string{
		"user@example.com",
		"user+tag@example.co.uk",
		"first.last@sub.domain.com",
	}
	for _, email := range emails {
		t.Run(email, func(t *testing.T) {
			id, err := NewIdentity(testTenantID, email)
			require.NoError(t, err)
			assert.NotEqual(t, uuid.Nil, id.ID())
			assert.Equal(t, email, id.Email())
			assert.Equal(t, IdentityStatusPendingInvite, id.Status())
			assert.Equal(t, int64(1), id.Version())
			assert.Equal(t, 0, id.FailedAttempts())
			assert.Empty(t, id.PasswordHash())
			assert.Empty(t, id.ExternalIDP())
			assert.Empty(t, id.ExternalSub())
			assert.NotZero(t, id.CreatedAt())
			assert.NotZero(t, id.UpdatedAt())
		})
	}
}

func TestNewIdentity_InvalidEmail(t *testing.T) {
	cases := []string{"", "not-an-email", "@nodomain", "no@", "spaces in@email.com"}
	for _, email := range cases {
		t.Run(email, func(t *testing.T) {
			_, err := NewIdentity(testTenantID, email)
			assert.ErrorIs(t, err, ErrInvalidEmail)
		})
	}
}

func TestIdentity_StatusTransitions(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*Identity)
		transition   func(*Identity) error
		expectedErr  error
		expectStatus IdentityStatus
	}{
		{
			name:         "activate from pending invite",
			setup:        func(_ *Identity) {},
			transition:   (*Identity).Activate,
			expectStatus: IdentityStatusActive,
		},
		{
			name: "activate from suspended",
			setup: func(id *Identity) {
				_ = id.Activate()
				_ = id.Suspend()
			},
			transition:   (*Identity).Activate,
			expectStatus: IdentityStatusActive,
		},
		{
			name: "activate from locked returns error",
			setup: func(id *Identity) {
				_ = id.Activate()
				_ = id.Lock()
			},
			transition:   (*Identity).Activate,
			expectedErr:  ErrInvalidStatusTransition,
			expectStatus: IdentityStatusLocked,
		},
		{
			name: "suspend from active",
			setup: func(id *Identity) {
				_ = id.Activate()
			},
			transition:   (*Identity).Suspend,
			expectStatus: IdentityStatusSuspended,
		},
		{
			name:         "suspend from pending invite returns error",
			setup:        func(_ *Identity) {},
			transition:   (*Identity).Suspend,
			expectedErr:  ErrInvalidStatusTransition,
			expectStatus: IdentityStatusPendingInvite,
		},
		{
			name: "lock from active",
			setup: func(id *Identity) {
				_ = id.Activate()
			},
			transition:   (*Identity).Lock,
			expectStatus: IdentityStatusLocked,
		},
		{
			name: "lock from suspended",
			setup: func(id *Identity) {
				_ = id.Activate()
				_ = id.Suspend()
			},
			transition:   (*Identity).Lock,
			expectStatus: IdentityStatusLocked,
		},
		{
			name:         "lock from pending invite returns error",
			setup:        func(_ *Identity) {},
			transition:   (*Identity).Lock,
			expectedErr:  ErrInvalidStatusTransition,
			expectStatus: IdentityStatusPendingInvite,
		},
		{
			name: "unlock from locked",
			setup: func(id *Identity) {
				_ = id.Activate()
				_ = id.Lock()
			},
			transition:   (*Identity).Unlock,
			expectStatus: IdentityStatusActive,
		},
		{
			name: "unlock from active returns error",
			setup: func(id *Identity) {
				_ = id.Activate()
			},
			transition:   (*Identity).Unlock,
			expectedErr:  ErrInvalidStatusTransition,
			expectStatus: IdentityStatusActive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity, err := NewIdentity(testTenantID, "test@example.com")
			require.NoError(t, err)

			tt.setup(identity)
			versionBefore := identity.Version()
			updatedBefore := identity.UpdatedAt()

			err = tt.transition(identity)

			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
				assert.Equal(t, versionBefore, identity.Version(), "version should not change on error")
			} else {
				require.NoError(t, err)
				assert.Greater(t, identity.Version(), versionBefore)
				assert.True(t, identity.UpdatedAt().Equal(updatedBefore) || identity.UpdatedAt().After(updatedBefore))
			}
			assert.Equal(t, tt.expectStatus, identity.Status())
		})
	}
}

func TestIdentity_IsLocked(t *testing.T) {
	identity, _ := NewIdentity(testTenantID, "user@example.com")
	assert.False(t, identity.IsLocked())

	_ = identity.Activate()
	_ = identity.Lock()
	assert.True(t, identity.IsLocked())

	_ = identity.Unlock()
	assert.False(t, identity.IsLocked())
}

func TestIdentity_RecordLoginAttempt_SuccessResetsCounter(t *testing.T) {
	identity, _ := NewIdentity(testTenantID, "user@example.com")
	_ = identity.Activate()

	// Record some failures first
	_ = identity.RecordLoginAttempt(false)
	_ = identity.RecordLoginAttempt(false)
	assert.Equal(t, 2, identity.FailedAttempts())

	// Success resets the counter
	_ = identity.RecordLoginAttempt(true)
	assert.Equal(t, 0, identity.FailedAttempts())
	assert.Equal(t, IdentityStatusActive, identity.Status())
}

func TestIdentity_RecordLoginAttempt_LockAfterFiveFailures(t *testing.T) {
	identity, _ := NewIdentity(testTenantID, "user@example.com")
	_ = identity.Activate()

	for i := 0; i < maxFailedAttempts-1; i++ {
		_ = identity.RecordLoginAttempt(false)
		assert.False(t, identity.IsLocked(), "should not be locked after %d failures", i+1)
	}

	// The 5th failure triggers lockout
	_ = identity.RecordLoginAttempt(false)
	assert.True(t, identity.IsLocked())
	assert.Equal(t, maxFailedAttempts, identity.FailedAttempts())
}

func TestIdentity_UnlockResetFailedAttempts(t *testing.T) {
	identity, _ := NewIdentity(testTenantID, "user@example.com")
	_ = identity.Activate()
	for i := 0; i < maxFailedAttempts; i++ {
		_ = identity.RecordLoginAttempt(false)
	}
	require.True(t, identity.IsLocked())

	err := identity.Unlock()
	require.NoError(t, err)
	assert.Equal(t, 0, identity.FailedAttempts())
	assert.Equal(t, IdentityStatusActive, identity.Status())
}

func TestIdentity_RecordLoginAttempt_GuardsNonActiveStatus(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Identity)
		expectedErr error
	}{
		{
			name:        "pending invite",
			setup:       func(_ *Identity) {},
			expectedErr: ErrInvalidStatusTransition,
		},
		{
			name: "suspended",
			setup: func(id *Identity) {
				_ = id.Activate()
				_ = id.Suspend()
			},
			expectedErr: ErrInvalidStatusTransition,
		},
		{
			name: "locked",
			setup: func(id *Identity) {
				_ = id.Activate()
				_ = id.Lock()
			},
			expectedErr: ErrAccountLocked,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity, _ := NewIdentity(testTenantID, "user@example.com")
			tt.setup(identity)
			err := identity.RecordLoginAttempt(false)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestIdentity_SetPassword(t *testing.T) {
	identity, _ := NewIdentity(testTenantID, "user@example.com")

	err := identity.SetPassword("")
	assert.ErrorIs(t, err, ErrPasswordHashEmpty)

	err = identity.SetPassword("$2a$12$somevalidhash")
	require.NoError(t, err)
	assert.Equal(t, "$2a$12$somevalidhash", identity.PasswordHash())
}

func TestIdentity_SetExternalIDP(t *testing.T) {
	identity, _ := NewIdentity(testTenantID, "user@example.com")

	err := identity.SetExternalIDP("", "some-sub")
	assert.ErrorIs(t, err, ErrExternalIDPEmpty)

	err = identity.SetExternalIDP("google", "")
	assert.ErrorIs(t, err, ErrExternalIDPEmpty)

	err = identity.SetExternalIDP("google", "12345")
	require.NoError(t, err)
	assert.Equal(t, "google", identity.ExternalIDP())
	assert.Equal(t, "12345", identity.ExternalSub())
}

func TestReconstructIdentity(t *testing.T) {
	id := uuid.New()
	now := time.Now()
	revokedAt := now.Add(time.Hour)

	identity := ReconstructIdentity(
		id, testTenantID, "user@example.com", IdentityStatusLocked,
		"$2a$12$hash", "github", "gh-sub",
		3, now, revokedAt, 5,
	)

	assert.Equal(t, id, identity.ID())
	assert.Equal(t, "user@example.com", identity.Email())
	assert.Equal(t, IdentityStatusLocked, identity.Status())
	assert.Equal(t, "$2a$12$hash", identity.PasswordHash())
	assert.Equal(t, "github", identity.ExternalIDP())
	assert.Equal(t, "gh-sub", identity.ExternalSub())
	assert.Equal(t, 3, identity.FailedAttempts())
	assert.Equal(t, int64(5), identity.Version())
	assert.Equal(t, testTenantID, identity.TenantID())
}

func TestNewIdentity_EmptyTenantID_ReturnsError(t *testing.T) {
	identity, err := NewIdentity("", "user@example.com")
	assert.Nil(t, identity)
	assert.ErrorIs(t, err, ErrTenantIDRequired)
}
