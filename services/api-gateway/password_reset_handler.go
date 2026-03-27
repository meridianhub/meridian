package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/pkg/tokens"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PasswordResetHandler errors.
var (
	ErrPasswordResetLoggerRequired   = errors.New("password reset handler: logger is required")
	ErrPasswordResetIdentityRequired = errors.New("password reset handler: identity repository is required")
	ErrPasswordResetOutboxRequired   = errors.New("password reset handler: email outbox repository is required")
)

// PasswordResetHandler handles password reset endpoints.
type PasswordResetHandler struct {
	identityRepo identitydomain.Repository
	outboxRepo   email.OutboxRepository
	baseDomain   string
	logger       *slog.Logger
}

// PasswordResetHandlerConfig holds dependencies for creating a PasswordResetHandler.
type PasswordResetHandlerConfig struct {
	IdentityRepo identitydomain.Repository
	OutboxRepo   email.OutboxRepository
	BaseDomain   string
	Logger       *slog.Logger
}

// NewPasswordResetHandler creates a handler for password reset endpoints.
func NewPasswordResetHandler(cfg PasswordResetHandlerConfig) (*PasswordResetHandler, error) {
	if cfg.Logger == nil {
		return nil, ErrPasswordResetLoggerRequired
	}
	if cfg.IdentityRepo == nil {
		return nil, ErrPasswordResetIdentityRequired
	}
	if cfg.OutboxRepo == nil {
		return nil, ErrPasswordResetOutboxRequired
	}
	return &PasswordResetHandler{
		identityRepo: cfg.IdentityRepo,
		outboxRepo:   cfg.OutboxRepo,
		baseDomain:   cfg.BaseDomain,
		logger:       cfg.Logger,
	}, nil
}

// forgotPasswordRequest is the JSON body for POST /api/v1/forgot-password.
type forgotPasswordRequest struct {
	Email    string `json:"email"`
	TenantID string `json:"tenant_id"`
}

// resetPasswordRequest is the JSON body for POST /api/v1/reset-password.
type resetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// maxPasswordResetTokensPerHour is the rate limit for password reset token issuance.
const maxPasswordResetTokensPerHour = 3

// HandleForgotPassword handles POST /api/v1/forgot-password.
func (h *PasswordResetHandler) HandleForgotPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req forgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Email == "" || req.TenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and tenant_id are required"})
		return
	}

	ctx := r.Context()

	tid, err := tenant.NewTenantID(req.TenantID)
	if err != nil {
		// Timing-safe: return 200 even for invalid tenant.
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	tenantCtx := tenant.WithTenant(ctx, tid)

	// Timing-safe: always return 200. Do not reveal whether the email exists.
	identity, err := h.identityRepo.FindByEmail(tenantCtx, req.Email)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Rate limit: max 3 tokens per hour per identity.
	count, err := h.identityRepo.CountPasswordResetTokensInWindow(tenantCtx, identity.ID(), time.Hour)
	if err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to count tokens in window", "error", err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if count >= maxPasswordResetTokensPerHour {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many password reset requests, please try again later"})
		return
	}

	ptoken, plaintext, err := identitydomain.NewPasswordResetToken(req.TenantID, identity.ID())
	if err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to create token", "error", err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	if err := h.identityRepo.SavePasswordResetToken(tenantCtx, ptoken); err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to save token", "error", err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	h.queuePasswordResetEmail(tenantCtx, identity.Email(), req.TenantID, plaintext)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleResetPassword handles POST /api/v1/reset-password.
func (h *PasswordResetHandler) HandleResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Token == "" || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token and new_password are required"})
		return
	}

	if err := credentials.ValidatePasswordPolicy(req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("password policy violation: %v", err),
		})
		return
	}

	ctx := r.Context()
	tokenHash := tokens.HashToken(req.Token)

	ptoken, err := h.identityRepo.FindPasswordResetTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, identitydomain.ErrPasswordResetTokenNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "password reset token not found"})
			return
		}
		h.logger.ErrorContext(ctx, "password-reset: failed to find token", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	if err := ptoken.Consume(); err != nil {
		if errors.Is(err, identitydomain.ErrPasswordResetTokenExpired) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password reset token has expired"})
			return
		}
		if errors.Is(err, identitydomain.ErrPasswordResetTokenAlreadyConsumed) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password reset token has already been consumed"})
			return
		}
		h.logger.ErrorContext(ctx, "password-reset: failed to consume token", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// Set tenant context for identity lookup.
	tid, err := tenant.NewTenantID(ptoken.TenantID())
	if err != nil {
		h.logger.ErrorContext(ctx, "password-reset: invalid tenant ID on token", "tenant_id", ptoken.TenantID(), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	tenantCtx := tenant.WithTenant(ctx, tid)

	identity, err := h.identityRepo.FindByID(tenantCtx, ptoken.IdentityID())
	if err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to find identity", "identity_id", ptoken.IdentityID(), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	hash, err := credentials.HashPassword(req.NewPassword)
	if err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to hash password", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	if err := identity.SetPassword(hash); err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to set password", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	if err := h.identityRepo.SavePasswordResetToken(tenantCtx, ptoken); err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to save consumed token", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	if err := h.identityRepo.Save(tenantCtx, identity); err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to save identity", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	// Invalidate all other reset tokens for this identity.
	if err := h.identityRepo.MarkPasswordResetTokensConsumedForIdentity(tenantCtx, ptoken.IdentityID()); err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to invalidate other tokens", "error", err)
		// Non-fatal: password was already changed successfully.
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "password_reset"})
}

// queuePasswordResetEmail enqueues a password reset email via the outbox.
func (h *PasswordResetHandler) queuePasswordResetEmail(ctx context.Context, emailAddr, tenantID, plaintoken string) {
	resetLink := h.buildResetLink(plaintoken)
	entry := &email.OutboxEntry{
		ID:             uuid.New(),
		TenantID:       tenantID,
		IdempotencyKey: "password-reset-" + uuid.New().String(),
		ToAddresses:    []string{emailAddr},
		FromAddress:    "noreply@meridianhub.cloud",
		Subject:        "Reset your password",
		TemplateName:   "password-reset",
		TemplateData: map[string]any{
			"ResetLink": resetLink,
		},
		Status:        email.StatusPending,
		MaxAttempts:   5,
		NextAttemptAt: time.Now(),
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := h.outboxRepo.Enqueue(ctx, entry); err != nil {
		h.logger.ErrorContext(ctx, "password-reset: failed to enqueue reset email", "error", err)
	}
}

func (h *PasswordResetHandler) buildResetLink(token string) string {
	if h.baseDomain != "" {
		return "https://" + h.baseDomain + "/reset-password?token=" + token
	}
	return "/reset-password?token=" + token
}

// WithPasswordResetHandler sets the password reset handler for the server.
func WithPasswordResetHandler(handler *PasswordResetHandler) ServerOption {
	return func(s *Server) {
		s.passwordResetHandler = handler
	}
}
