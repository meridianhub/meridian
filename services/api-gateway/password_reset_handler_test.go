package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	gateway "github.com/meridianhub/meridian/services/api-gateway"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Helpers ---

func newPasswordResetHandler(t *testing.T, ir identitydomain.Repository, or email.OutboxRepository) *gateway.PasswordResetHandler {
	t.Helper()
	h, err := gateway.NewPasswordResetHandler(gateway.PasswordResetHandlerConfig{
		IdentityRepo: ir,
		OutboxRepo:   or,
		BaseDomain:   "meridian.app",
		Logger:       slog.Default(),
	})
	require.NoError(t, err)
	return h
}

func postForgotPassword(handler *gateway.PasswordResetHandler, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/forgot-password", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleForgotPassword(w, r)
	return w
}

func postResetPassword(handler *gateway.PasswordResetHandler, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/reset-password", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleResetPassword(w, r)
	return w
}

// --- Tests: HandleForgotPassword ---

func TestForgotPassword_Success(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	identity := identitydomain.ReconstructIdentity(
		identityID, tid, "user@test.com",
		identitydomain.IdentityStatusActive,
		"$2a$12$hashhere", "", "", 0,
		time.Now(), time.Now(), 1,
	)

	ir := &stubIdentityRepo{
		findByEmailFn: func(_ context.Context, em string) (*identitydomain.Identity, error) {
			if em == "user@test.com" {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
		countPasswordResetTokensInWindowFn: func(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
			return 0, nil
		},
	}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postForgotPassword(h, map[string]string{
		"email":     "user@test.com",
		"tenant_id": "test_tenant",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Password reset email should be queued.
	assert.Len(t, or.entries, 1)
	assert.Equal(t, "password-reset", or.entries[0].TemplateName)
}

func TestForgotPassword_TimingSafe_NonexistentEmail(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postForgotPassword(h, map[string]string{
		"email":     "nonexistent@test.com",
		"tenant_id": "test_tenant",
	})
	// Must return 200 even for non-existent email.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, or.entries)
}

func TestForgotPassword_RateLimited(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	identity := identitydomain.ReconstructIdentity(
		identityID, tid, "user@test.com",
		identitydomain.IdentityStatusActive,
		"$2a$12$hashhere", "", "", 0,
		time.Now(), time.Now(), 1,
	)

	ir := &stubIdentityRepo{
		findByEmailFn: func(_ context.Context, _ string) (*identitydomain.Identity, error) {
			return identity, nil
		},
		countPasswordResetTokensInWindowFn: func(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
			return 3, nil // at the limit
		},
	}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postForgotPassword(h, map[string]string{
		"email":     "user@test.com",
		"tenant_id": "test_tenant",
	})
	// Timing-safe: returns 200 even when rate limited to avoid email enumeration.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, or.entries)
}

func TestForgotPassword_MissingFields(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postForgotPassword(h, map[string]string{"email": "user@test.com"})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = postForgotPassword(h, map[string]string{"tenant_id": "test_tenant"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestForgotPassword_MethodNotAllowed(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/forgot-password", nil)
	w := httptest.NewRecorder()
	h.HandleForgotPassword(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- Tests: HandleResetPassword ---

func TestResetPassword_Success(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	ptoken, plaintext, err := identitydomain.NewPasswordResetToken("test_tenant", identityID)
	require.NoError(t, err)

	identity := identitydomain.ReconstructIdentity(
		identityID, tid, "user@test.com",
		identitydomain.IdentityStatusActive,
		"$2a$12$hashhere", "", "", 0,
		time.Now(), time.Now(), 1,
	)

	var passwordUpdated bool
	ir := &stubIdentityRepo{
		findPasswordResetTokenByHashFn: func(_ context.Context, hash string) (*identitydomain.PasswordResetToken, error) {
			if hash == tokens.HashToken(plaintext) {
				return ptoken, nil
			}
			return nil, identitydomain.ErrPasswordResetTokenNotFound
		},
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
		saveFn: func(_ context.Context, _ *identitydomain.Identity) error {
			passwordUpdated = true
			return nil
		},
	}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postResetPassword(h, map[string]string{
		"token":        plaintext,
		"new_password": "NewSecurePass123!",
	})

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseResponse(t, w)
	assert.Equal(t, "password_reset", resp["status"])
	assert.True(t, passwordUpdated)
}

func TestResetPassword_MissingFields(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postResetPassword(h, map[string]string{"token": "abc"})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = postResetPassword(h, map[string]string{"new_password": "NewSecurePass123!"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResetPassword_WeakPassword(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postResetPassword(h, map[string]string{
		"token":        "some-token",
		"new_password": "weak",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "password")
}

func TestResetPassword_TokenNotFound(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postResetPassword(h, map[string]string{
		"token":        "nonexistent",
		"new_password": "NewSecurePass123!",
	})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestResetPassword_TokenExpired(t *testing.T) {
	identityID := uuid.New()

	expiredToken := identitydomain.ReconstructPasswordResetToken(
		uuid.New(), "test_tenant", identityID,
		tokens.HashToken("expired-token"),
		time.Now().Add(-1*time.Hour), // expired
		nil,
		time.Now().Add(-2*time.Hour),
	)

	ir := &stubIdentityRepo{
		findPasswordResetTokenByHashFn: func(_ context.Context, _ string) (*identitydomain.PasswordResetToken, error) {
			return expiredToken, nil
		},
	}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postResetPassword(h, map[string]string{
		"token":        "expired-token",
		"new_password": "NewSecurePass123!",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "expired")
}

func TestResetPassword_TokenAlreadyConsumed(t *testing.T) {
	identityID := uuid.New()
	consumedAt := time.Now()

	consumedToken := identitydomain.ReconstructPasswordResetToken(
		uuid.New(), "test_tenant", identityID,
		tokens.HashToken("consumed-token"),
		time.Now().Add(1*time.Hour),
		&consumedAt,
		time.Now(),
	)

	ir := &stubIdentityRepo{
		findPasswordResetTokenByHashFn: func(_ context.Context, _ string) (*identitydomain.PasswordResetToken, error) {
			return consumedToken, nil
		},
	}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	w := postResetPassword(h, map[string]string{
		"token":        "consumed-token",
		"new_password": "NewSecurePass123!",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "already been consumed")
}

func TestResetPassword_MethodNotAllowed(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newPasswordResetHandler(t, ir, or)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/reset-password", nil)
	w := httptest.NewRecorder()
	h.HandleResetPassword(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- Constructor Validation ---

func TestNewPasswordResetHandler_Validation(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}

	_, err := gateway.NewPasswordResetHandler(gateway.PasswordResetHandlerConfig{
		Logger: slog.Default(),
	})
	assert.ErrorIs(t, err, gateway.ErrPasswordResetIdentityRequired)

	_, err = gateway.NewPasswordResetHandler(gateway.PasswordResetHandlerConfig{
		IdentityRepo: ir,
		Logger:       slog.Default(),
	})
	assert.ErrorIs(t, err, gateway.ErrPasswordResetOutboxRequired)

	_, err = gateway.NewPasswordResetHandler(gateway.PasswordResetHandlerConfig{
		IdentityRepo: ir,
		OutboxRepo:   or,
	})
	assert.ErrorIs(t, err, gateway.ErrPasswordResetLoggerRequired)
}
