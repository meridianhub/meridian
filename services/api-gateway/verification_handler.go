package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// VerificationHandler errors.
var (
	ErrVerificationLoggerRequired   = errors.New("verification handler: logger is required")
	ErrVerificationIdentityRequired = errors.New("verification handler: identity repository is required")
	ErrVerificationOutboxRequired   = errors.New("verification handler: email outbox repository is required")
)

// VerificationHandler handles email verification endpoints.
type VerificationHandler struct {
	identityRepo identitydomain.Repository
	outboxRepo   email.OutboxRepository
	baseDomain   string
	logger       *slog.Logger
}

// VerificationHandlerConfig holds dependencies for creating a VerificationHandler.
type VerificationHandlerConfig struct {
	IdentityRepo identitydomain.Repository
	OutboxRepo   email.OutboxRepository
	BaseDomain   string
	Logger       *slog.Logger
}

// NewVerificationHandler creates a handler for email verification endpoints.
func NewVerificationHandler(cfg VerificationHandlerConfig) (*VerificationHandler, error) {
	if cfg.Logger == nil {
		return nil, ErrVerificationLoggerRequired
	}
	if cfg.IdentityRepo == nil {
		return nil, ErrVerificationIdentityRequired
	}
	if cfg.OutboxRepo == nil {
		return nil, ErrVerificationOutboxRequired
	}
	return &VerificationHandler{
		identityRepo: cfg.IdentityRepo,
		outboxRepo:   cfg.OutboxRepo,
		baseDomain:   cfg.BaseDomain,
		logger:       cfg.Logger,
	}, nil
}

// verifyEmailRequest is the JSON body for POST /api/v1/verify-email.
type verifyEmailRequest struct {
	Token string `json:"token"`
}

// resendVerificationRequest is the JSON body for POST /api/v1/resend-verification.
type resendVerificationRequest struct {
	Email    string `json:"email"`
	TenantID string `json:"tenant_id"`
}

// maxVerificationTokensPerHour is the rate limit for verification token issuance.
const maxVerificationTokensPerHour = 3

// HandleVerifyEmail handles POST /api/v1/verify-email.
func (h *VerificationHandler) HandleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req verifyEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}

	status, resp := h.executeVerifyEmail(r.Context(), req.Token)
	writeJSON(w, status, resp)
}

// executeVerifyEmail performs the token lookup, consumption, identity verification, and persistence.
func (h *VerificationHandler) executeVerifyEmail(ctx context.Context, rawToken string) (int, map[string]string) {
	tokenHash := tokens.HashToken(rawToken)

	vtoken, err := h.identityRepo.FindVerificationTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, identitydomain.ErrVerificationTokenNotFound) {
			return http.StatusNotFound, map[string]string{"error": "verification token not found"}
		}
		h.logger.ErrorContext(ctx, "verification: failed to find token", "error", err)
		return http.StatusInternalServerError, map[string]string{"error": "internal server error"}
	}

	if err := vtoken.Consume(); err != nil {
		return h.handleConsumeError(ctx, err)
	}

	tid, err := tenant.NewTenantID(vtoken.TenantID())
	if err != nil {
		h.logger.ErrorContext(ctx, "verification: invalid tenant ID on token", "tenant_id", vtoken.TenantID(), "error", err)
		return http.StatusInternalServerError, map[string]string{"error": "internal server error"}
	}
	tenantCtx := tenant.WithTenant(ctx, tid)

	identity, err := h.identityRepo.FindByID(tenantCtx, vtoken.IdentityID())
	if err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to find identity", "identity_id", vtoken.IdentityID(), "error", err)
		return http.StatusInternalServerError, map[string]string{"error": "internal server error"}
	}

	if err := identity.Verify(); err != nil {
		if errors.Is(err, identitydomain.ErrNotPendingVerification) {
			return http.StatusBadRequest, map[string]string{"error": "identity is not pending verification"}
		}
		h.logger.ErrorContext(ctx, "verification: failed to verify identity", "error", err)
		return http.StatusInternalServerError, map[string]string{"error": "internal server error"}
	}

	// Save identity first so the token remains valid for retry if this fails.
	if err := h.identityRepo.Save(tenantCtx, identity); err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to save verified identity", "error", err)
		return http.StatusInternalServerError, map[string]string{"error": "internal server error"}
	}

	// Mark token consumed after identity is persisted (safe to retry on failure).
	if err := h.identityRepo.SaveVerificationToken(tenantCtx, vtoken); err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to save consumed token", "error", err)
		// Non-fatal: identity is already verified. Worst case: token is reusable
		// but Verify() will return ErrNotPendingVerification on reuse.
	}

	h.queueWelcomeEmail(tenantCtx, identity)
	return http.StatusOK, map[string]string{"status": "verified"}
}

// handleConsumeError maps verification token Consume errors to HTTP responses.
func (h *VerificationHandler) handleConsumeError(ctx context.Context, err error) (int, map[string]string) {
	if errors.Is(err, identitydomain.ErrVerificationTokenExpired) {
		return http.StatusBadRequest, map[string]string{"error": "verification token has expired"}
	}
	if errors.Is(err, identitydomain.ErrVerificationTokenAlreadyConsumed) {
		return http.StatusBadRequest, map[string]string{"error": "verification token has already been consumed"}
	}
	h.logger.ErrorContext(ctx, "verification: failed to consume token", "error", err)
	return http.StatusInternalServerError, map[string]string{"error": "internal server error"}
}

// HandleResendVerification handles POST /api/v1/resend-verification.
func (h *VerificationHandler) HandleResendVerification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req resendVerificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Email == "" || req.TenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and tenant_id are required"})
		return
	}

	h.issueResendVerificationToken(r.Context(), req)

	// Timing-safe: always return 200 regardless of outcome.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// issueResendVerificationToken performs the timing-safe verification resend flow.
// All error paths are silent (logged but not exposed) to prevent email enumeration.
func (h *VerificationHandler) issueResendVerificationToken(ctx context.Context, req resendVerificationRequest) {
	tid, err := tenant.NewTenantID(req.TenantID)
	if err != nil {
		return
	}
	tenantCtx := tenant.WithTenant(ctx, tid)

	identity, err := h.identityRepo.FindByEmail(tenantCtx, req.Email)
	if err != nil {
		if !errors.Is(err, identitydomain.ErrIdentityNotFound) {
			h.logger.ErrorContext(ctx, "verification: failed to find identity by email", "error", err)
		}
		return
	}

	if identity.Status() != identitydomain.IdentityStatusPendingVerification {
		return
	}

	count, err := h.identityRepo.CountVerificationTokensInWindow(tenantCtx, identity.ID(), time.Hour)
	if err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to count tokens in window", "error", err)
		return
	}
	if count >= maxVerificationTokensPerHour {
		h.logger.WarnContext(ctx, "verification: resend rate limited", "identity_id", identity.ID())
		return
	}

	vtoken, plaintext, err := identitydomain.NewVerificationToken(req.TenantID, identity.ID())
	if err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to create token", "error", err)
		return
	}

	if err := h.identityRepo.SaveVerificationToken(tenantCtx, vtoken); err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to save token", "error", err)
		return
	}

	h.queueVerificationEmail(tenantCtx, identity.Email(), req.TenantID, plaintext)
}

// queueVerificationEmail enqueues a verification email via the outbox.
func (h *VerificationHandler) queueVerificationEmail(ctx context.Context, emailAddr, tenantID, plaintoken string) {
	verificationLink := h.buildVerificationLink(plaintoken)
	entry := &email.OutboxEntry{
		ID:             uuid.New(),
		TenantID:       tenantID,
		IdempotencyKey: "verify-email-" + uuid.New().String(),
		ToAddresses:    []string{emailAddr},
		FromAddress:    "noreply@meridianhub.cloud",
		Subject:        "Verify your email address",
		TemplateName:   "verify-email",
		TemplateData: map[string]any{
			"VerificationLink": verificationLink,
		},
		Status:        email.StatusPending,
		MaxAttempts:   5,
		NextAttemptAt: time.Now(),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := h.outboxRepo.Enqueue(ctx, entry); err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to enqueue verification email", "error", err)
	}
}

// queueWelcomeEmail enqueues a welcome email via the outbox.
func (h *VerificationHandler) queueWelcomeEmail(ctx context.Context, identity *identitydomain.Identity) {
	loginURL := h.buildLoginURL(identity.TenantID().String())
	entry := &email.OutboxEntry{
		ID:             uuid.New(),
		TenantID:       identity.TenantID().String(),
		IdempotencyKey: "welcome-" + identity.ID().String(),
		ToAddresses:    []string{identity.Email()},
		FromAddress:    "noreply@meridianhub.cloud",
		Subject:        "Welcome to Meridian",
		TemplateName:   "welcome",
		TemplateData: map[string]any{
			"LoginURL": loginURL,
		},
		Status:        email.StatusPending,
		MaxAttempts:   5,
		NextAttemptAt: time.Now(),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := h.outboxRepo.Enqueue(ctx, entry); err != nil {
		h.logger.ErrorContext(ctx, "verification: failed to enqueue welcome email", "error", err)
	}
}

func (h *VerificationHandler) buildVerificationLink(token string) string {
	if h.baseDomain != "" {
		return "https://" + h.baseDomain + "/verify-email?token=" + token
	}
	return "/verify-email?token=" + token
}

func (h *VerificationHandler) buildLoginURL(tenantID string) string {
	if h.baseDomain != "" {
		return "https://" + h.baseDomain + "/login"
	}
	return "/login?tenant=" + tenantID
}

// WithVerificationHandler sets the email verification handler for the server.
func WithVerificationHandler(handler *VerificationHandler) ServerOption {
	return func(s *Server) {
		s.verificationHandler = handler
	}
}
