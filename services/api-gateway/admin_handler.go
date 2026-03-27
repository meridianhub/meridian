package gateway

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	gwauth "github.com/meridianhub/meridian/services/api-gateway/auth"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

// AdminHandler errors.
var (
	ErrAdminLoggerRequired   = errors.New("admin handler: logger is required")
	ErrAdminIdentityRequired = errors.New("admin handler: identity repository is required")
)

// AdminHandler handles admin override operations for identity management.
// POST /api/v1/admin/identities/{identity_id}/verify
type AdminHandler struct {
	identityRepo identitydomain.Repository
	logger       *slog.Logger
}

// NewAdminHandler creates a handler for admin identity override operations.
func NewAdminHandler(identityRepo identitydomain.Repository, logger *slog.Logger) (*AdminHandler, error) {
	if logger == nil {
		return nil, ErrAdminLoggerRequired
	}
	if identityRepo == nil {
		return nil, ErrAdminIdentityRequired
	}
	return &AdminHandler{
		identityRepo: identityRepo,
		logger:       logger,
	}, nil
}

// HandleVerifyOverride handles POST /api/v1/admin/identities/{identity_id}/verify.
// Manually verifies an identity, bypassing email verification.
// Transitions PENDING_VERIFICATION or PENDING_INVITE to ACTIVE.
// Requires admin role (tenant-owner, platform-admin, or super-admin).
func (h *AdminHandler) HandleVerifyOverride(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claims, ok := gwauth.GetClaimsFromContext(ctx)
	if !ok || !isAdminRole(claims) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}

	identityIDStr := r.PathValue("identity_id")
	identityID, err := uuid.Parse(identityIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid identity_id"})
		return
	}

	identity, err := h.identityRepo.FindByID(ctx, identityID)
	if err != nil {
		if errors.Is(err, identitydomain.ErrIdentityNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "identity not found"})
			return
		}
		h.logger.ErrorContext(ctx, "admin verify override: failed to find identity",
			"identity_id", identityID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load identity"})
		return
	}

	previousStatus := identity.Status()
	adminID, _ := gwauth.GetUserIDFromContext(ctx)

	switch identity.Status() {
	case identitydomain.IdentityStatusActive:
		h.logger.InfoContext(ctx, "admin verify override: identity already active",
			"identity_id", identityID,
			"admin_id", adminID)
		writeJSON(w, http.StatusOK, map[string]string{"status": string(identity.Status())})
		return

	case identitydomain.IdentityStatusPendingVerification:
		if err := identity.Verify(); err != nil {
			h.logger.ErrorContext(ctx, "admin verify override: failed to verify identity",
				"identity_id", identityID,
				"error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify identity"})
			return
		}

	case identitydomain.IdentityStatusPendingInvite:
		if err := identity.Activate(); err != nil {
			h.logger.ErrorContext(ctx, "admin verify override: failed to activate identity",
				"identity_id", identityID,
				"error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to activate identity"})
			return
		}

	default:
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "cannot override identity in current status",
		})
		return
	}

	if err := h.identityRepo.Save(ctx, identity); err != nil {
		h.logger.ErrorContext(ctx, "admin verify override: failed to save identity",
			"identity_id", identityID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save identity"})
		return
	}

	h.logger.InfoContext(ctx, "admin verify override: identity activated",
		"identity_id", identityID,
		"admin_id", adminID,
		"previous_status", string(previousStatus),
		"new_status", string(identity.Status()))

	writeJSON(w, http.StatusOK, map[string]string{"status": string(identity.Status())})
}

// isAdminRole returns true if claims contain a tenant-owner, platform-admin, or super-admin role.
func isAdminRole(claims *platformauth.Claims) bool {
	if claims == nil {
		return false
	}
	return claims.HasRole(platformauth.RoleTenantOwner.String()) ||
		claims.HasRole(platformauth.RolePlatformAdmin.String()) ||
		claims.HasRole(platformauth.RoleSuperAdmin.String())
}

// WithAdminHandler sets the admin handler for the server.
func WithAdminHandler(handler *AdminHandler) ServerOption {
	return func(s *Server) {
		s.adminHandler = handler
	}
}
