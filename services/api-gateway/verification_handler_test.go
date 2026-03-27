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

// --- Stub outbox repo ---

type stubOutboxRepo struct {
	enqueueFn func(ctx context.Context, entry *email.OutboxEntry) error
	entries   []*email.OutboxEntry
}

func (s *stubOutboxRepo) Enqueue(ctx context.Context, entry *email.OutboxEntry) error {
	s.entries = append(s.entries, entry)
	if s.enqueueFn != nil {
		return s.enqueueFn(ctx, entry)
	}
	return nil
}

func (s *stubOutboxRepo) FetchDispatchable(_ context.Context, _ int) ([]email.OutboxEntry, error) {
	return nil, nil
}

func (s *stubOutboxRepo) MarkSent(_ context.Context, _ uuid.UUID) error { return nil }

func (s *stubOutboxRepo) MarkFailed(_ context.Context, _ uuid.UUID, _ string) error { return nil }

func (s *stubOutboxRepo) Cancel(_ context.Context, _ uuid.UUID) error { return nil }

func (s *stubOutboxRepo) CancelByIdempotencyKeyPattern(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

// --- Helpers ---

func newVerificationHandler(t *testing.T, ir identitydomain.Repository, or email.OutboxRepository) *gateway.VerificationHandler {
	t.Helper()
	h, err := gateway.NewVerificationHandler(gateway.VerificationHandlerConfig{
		IdentityRepo: ir,
		OutboxRepo:   or,
		BaseDomain:   "meridian.app",
		Logger:       slog.Default(),
	})
	require.NoError(t, err)
	return h
}

func postVerifyEmail(handler *gateway.VerificationHandler, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/verify-email", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleVerifyEmail(w, r)
	return w
}

func postResendVerification(handler *gateway.VerificationHandler, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/resend-verification", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleResendVerification(w, r)
	return w
}

// --- Tests: HandleVerifyEmail ---

func TestVerifyEmail_Success(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	// Create a verification token.
	vtoken, plaintext, err := identitydomain.NewVerificationToken("test_tenant", identityID)
	require.NoError(t, err)

	identity := identitydomain.ReconstructIdentity(
		identityID, tid, "user@test.com",
		identitydomain.IdentityStatusPendingVerification,
		"$2a$12$hashhere", "", "", 0,
		time.Now(), time.Now(), 1,
	)

	ir := &stubIdentityRepo{
		findVerificationTokenByHashFn: func(_ context.Context, hash string) (*identitydomain.VerificationToken, error) {
			if hash == tokens.HashToken(plaintext) {
				return vtoken, nil
			}
			return nil, identitydomain.ErrVerificationTokenNotFound
		},
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
	}
	or := &stubOutboxRepo{}

	h := newVerificationHandler(t, ir, or)
	w := postVerifyEmail(h, map[string]string{"token": plaintext})

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseResponse(t, w)
	assert.Equal(t, "verified", resp["status"])

	// Welcome email should be queued.
	assert.Len(t, or.entries, 1)
	assert.Equal(t, "welcome", or.entries[0].TemplateName)
}

func TestVerifyEmail_MissingToken(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postVerifyEmail(h, map[string]string{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestVerifyEmail_TokenNotFound(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postVerifyEmail(h, map[string]string{"token": "nonexistent"})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestVerifyEmail_TokenExpired(t *testing.T) {
	tid := "test_tenant"
	identityID := uuid.New()

	// Create an expired token.
	expiredToken := identitydomain.ReconstructVerificationToken(
		uuid.New(), tid, identityID,
		tokens.HashToken("expired-token"),
		time.Now().Add(-1*time.Hour), // expired
		nil,
		time.Now().Add(-25*time.Hour),
	)

	ir := &stubIdentityRepo{
		findVerificationTokenByHashFn: func(_ context.Context, _ string) (*identitydomain.VerificationToken, error) {
			return expiredToken, nil
		},
	}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postVerifyEmail(h, map[string]string{"token": "expired-token"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "expired")
}

func TestVerifyEmail_TokenAlreadyConsumed(t *testing.T) {
	tid := "test_tenant"
	identityID := uuid.New()
	consumedAt := time.Now()

	consumedToken := identitydomain.ReconstructVerificationToken(
		uuid.New(), tid, identityID,
		tokens.HashToken("consumed-token"),
		time.Now().Add(24*time.Hour),
		&consumedAt,
		time.Now(),
	)

	ir := &stubIdentityRepo{
		findVerificationTokenByHashFn: func(_ context.Context, _ string) (*identitydomain.VerificationToken, error) {
			return consumedToken, nil
		},
	}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postVerifyEmail(h, map[string]string{"token": "consumed-token"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "already been consumed")
}

func TestVerifyEmail_IdentityNotPendingVerification(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	vtoken, plaintext, _ := identitydomain.NewVerificationToken("test_tenant", identityID)

	// Identity is already ACTIVE.
	identity := identitydomain.ReconstructIdentity(
		identityID, tid, "user@test.com",
		identitydomain.IdentityStatusActive,
		"$2a$12$hashhere", "", "", 0,
		time.Now(), time.Now(), 1,
	)

	ir := &stubIdentityRepo{
		findVerificationTokenByHashFn: func(_ context.Context, hash string) (*identitydomain.VerificationToken, error) {
			if hash == tokens.HashToken(plaintext) {
				return vtoken, nil
			}
			return nil, identitydomain.ErrVerificationTokenNotFound
		},
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
	}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postVerifyEmail(h, map[string]string{"token": plaintext})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "not pending verification")
}

func TestVerifyEmail_MethodNotAllowed(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/verify-email", nil)
	w := httptest.NewRecorder()
	h.HandleVerifyEmail(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- Tests: HandleResendVerification ---

func TestResendVerification_Success(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	identity := identitydomain.ReconstructIdentity(
		identityID, tid, "user@test.com",
		identitydomain.IdentityStatusPendingVerification,
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
		countVerificationTokensInWindowFn: func(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
			return 0, nil
		},
	}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postResendVerification(h, map[string]string{
		"email":     "user@test.com",
		"tenant_id": "test_tenant",
	})
	assert.Equal(t, http.StatusOK, w.Code)

	// Verification email should be queued.
	assert.Len(t, or.entries, 1)
	assert.Equal(t, "verify-email", or.entries[0].TemplateName)
}

func TestResendVerification_TimingSafe_NonexistentEmail(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postResendVerification(h, map[string]string{
		"email":     "nonexistent@test.com",
		"tenant_id": "test_tenant",
	})
	// Must return 200 even for non-existent email.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, or.entries)
}

func TestResendVerification_TimingSafe_ActiveIdentity(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	// Identity is ACTIVE, not pending verification.
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
	}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postResendVerification(h, map[string]string{
		"email":     "user@test.com",
		"tenant_id": "test_tenant",
	})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, or.entries)
}

func TestResendVerification_RateLimited(t *testing.T) {
	tid, _ := tenant.NewTenantID("test_tenant")
	identityID := uuid.New()

	identity := identitydomain.ReconstructIdentity(
		identityID, tid, "user@test.com",
		identitydomain.IdentityStatusPendingVerification,
		"$2a$12$hashhere", "", "", 0,
		time.Now(), time.Now(), 1,
	)

	ir := &stubIdentityRepo{
		findByEmailFn: func(_ context.Context, _ string) (*identitydomain.Identity, error) {
			return identity, nil
		},
		countVerificationTokensInWindowFn: func(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
			return 3, nil // at the limit
		},
	}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postResendVerification(h, map[string]string{
		"email":     "user@test.com",
		"tenant_id": "test_tenant",
	})
	// Timing-safe: returns 200 even when rate limited to avoid email enumeration.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, or.entries)
}

func TestResendVerification_MissingFields(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	w := postResendVerification(h, map[string]string{"email": "user@test.com"})
	assert.Equal(t, http.StatusBadRequest, w.Code)

	w = postResendVerification(h, map[string]string{"tenant_id": "test_tenant"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResendVerification_MethodNotAllowed(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}
	h := newVerificationHandler(t, ir, or)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/resend-verification", nil)
	w := httptest.NewRecorder()
	h.HandleResendVerification(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- Constructor Validation ---

func TestNewVerificationHandler_Validation(t *testing.T) {
	ir := &stubIdentityRepo{}
	or := &stubOutboxRepo{}

	_, err := gateway.NewVerificationHandler(gateway.VerificationHandlerConfig{
		Logger: slog.Default(),
	})
	assert.ErrorIs(t, err, gateway.ErrVerificationIdentityRequired)

	_, err = gateway.NewVerificationHandler(gateway.VerificationHandlerConfig{
		IdentityRepo: ir,
		Logger:       slog.Default(),
	})
	assert.ErrorIs(t, err, gateway.ErrVerificationOutboxRequired)

	_, err = gateway.NewVerificationHandler(gateway.VerificationHandlerConfig{
		IdentityRepo: ir,
		OutboxRepo:   or,
	})
	assert.ErrorIs(t, err, gateway.ErrVerificationLoggerRequired)
}
