package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	tenantdomain "github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// RegistrationHandler errors.
var (
	ErrRegistrationLoggerRequired  = errors.New("registration handler: logger is required")
	ErrRegistrationTenantRequired  = errors.New("registration handler: tenant creator is required")
	ErrRegistrationIdentityRequired = errors.New("registration handler: identity repository is required")
)

// TenantCreator abstracts tenant creation for the registration handler.
type TenantCreator interface {
	// CreateTenant creates a new tenant with the given ID, slug, and display name.
	// Returns the tenant ID on success.
	CreateTenant(ctx context.Context, tenantID, slug, displayName string) (string, error)
	// IsSlugAvailable checks whether a slug is available for use.
	IsSlugAvailable(ctx context.Context, slug string) (bool, error)
}

// RegistrationHandler handles self-service tenant registration.
// POST /api/v1/register - creates a tenant and an initial admin identity.
type RegistrationHandler struct {
	tenantCreator TenantCreator
	identityRepo  identitydomain.Repository
	rateLimiter   *RegistrationRateLimiter
	baseDomain    string
	logger        *slog.Logger
}

// RegistrationHandlerConfig holds dependencies for creating a RegistrationHandler.
type RegistrationHandlerConfig struct {
	TenantCreator TenantCreator
	IdentityRepo  identitydomain.Repository
	RateLimiter   *RegistrationRateLimiter
	BaseDomain    string
	Logger        *slog.Logger
}

// NewRegistrationHandler creates a handler for self-service tenant registration.
func NewRegistrationHandler(cfg RegistrationHandlerConfig) (*RegistrationHandler, error) {
	if cfg.Logger == nil {
		return nil, ErrRegistrationLoggerRequired
	}
	if cfg.TenantCreator == nil {
		return nil, ErrRegistrationTenantRequired
	}
	if cfg.IdentityRepo == nil {
		return nil, ErrRegistrationIdentityRequired
	}
	rl := cfg.RateLimiter
	if rl == nil {
		rl = NewRegistrationRateLimiter(5)
	}
	return &RegistrationHandler{
		tenantCreator: cfg.TenantCreator,
		identityRepo:  cfg.IdentityRepo,
		rateLimiter:   rl,
		baseDomain:    cfg.BaseDomain,
		logger:        cfg.Logger,
	}, nil
}

// registrationRequest is the JSON body for POST /api/v1/register.
type registrationRequest struct {
	Slug        string `json:"slug"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// registrationResponse is the JSON body returned on successful registration.
type registrationResponse struct {
	TenantID string `json:"tenant_id"`
	LoginURL string `json:"login_url"`
}

// HandleRegister handles POST /api/v1/register.
func (h *RegistrationHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rate limit by client IP.
	clientIP := ClientIP(r)
	if !h.rateLimiter.Allow(clientIP) {
		h.logger.WarnContext(r.Context(), "registration rate limited",
			"client_ip", clientIP)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "too many registration attempts, please try again later",
		})
		return
	}

	// Limit request body to 4KB.
	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req registrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid request body",
		})
		return
	}

	// Validate required fields.
	if req.Slug == "" || req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "slug, email, and password are required",
		})
		return
	}

	// Validate slug format.
	if err := tenantdomain.ValidateSlug(req.Slug); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid slug: %v", err),
		})
		return
	}

	// Validate password policy.
	if err := credentials.ValidatePasswordPolicy(req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("password policy violation: %v", err),
		})
		return
	}

	ctx := r.Context()

	// Check slug availability.
	available, err := h.tenantCreator.IsSlugAvailable(ctx, req.Slug)
	if err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to check slug availability",
			"slug", req.Slug,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to check slug availability",
		})
		return
	}
	if !available {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "slug is already taken",
		})
		return
	}

	// Derive tenant ID from slug (replace hyphens with underscores for DB schema naming).
	tenantID := strings.ReplaceAll(req.Slug, "-", "_")

	// Default display name to slug if not provided.
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Slug
	}

	// Create tenant.
	createdTenantID, err := h.tenantCreator.CreateTenant(ctx, tenantID, req.Slug, displayName)
	if err != nil {
		if isAlreadyExistsError(err) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "tenant already exists",
			})
			return
		}
		h.logger.ErrorContext(ctx, "registration: failed to create tenant",
			"tenant_id", tenantID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create tenant",
		})
		return
	}

	// Create admin identity within the new tenant's scope.
	tid, err := tenant.NewTenantID(createdTenantID)
	if err != nil {
		h.logger.ErrorContext(ctx, "registration: invalid tenant ID from creation",
			"tenant_id", createdTenantID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to initialize tenant identity",
		})
		return
	}

	tenantCtx := tenant.WithTenant(ctx, tid)

	identity, err := identitydomain.NewIdentity(req.Email)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid email: %v", err),
		})
		return
	}

	hash, err := credentials.HashPassword(req.Password)
	if err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to hash password",
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create identity",
		})
		return
	}

	if err := identity.SetPassword(hash); err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to set password",
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create identity",
		})
		return
	}

	if err := identity.Activate(); err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to activate identity",
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create identity",
		})
		return
	}

	// Assign TENANT_OWNER role to the admin identity.
	now := time.Now()
	ra := identitydomain.ReconstructRoleAssignment(
		uuid.New(),
		identity.ID(),
		identity.ID(), // self-granted (system bootstrap)
		identitydomain.RoleTenantOwner,
		nil, nil, nil,
		now, now,
	)

	if err := h.identityRepo.SaveIdentityWithRoles(tenantCtx, identity, []*identitydomain.RoleAssignment{ra}); err != nil {
		if errors.Is(err, identitydomain.ErrEmailAlreadyExists) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "email already registered in this tenant",
			})
			return
		}
		h.logger.ErrorContext(ctx, "registration: failed to save identity",
			"tenant_id", createdTenantID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create identity",
		})
		return
	}

	h.logger.InfoContext(ctx, "registration: tenant and admin identity created",
		"tenant_id", createdTenantID,
		"slug", req.Slug,
		"identity_id", identity.ID(),
		"client_ip", clientIP)

	loginURL := fmt.Sprintf("https://%s.%s/login", req.Slug, h.baseDomain)
	if h.baseDomain == "" {
		loginURL = fmt.Sprintf("/login?tenant=%s", req.Slug)
	}

	writeJSON(w, http.StatusCreated, registrationResponse{
		TenantID: createdTenantID,
		LoginURL: loginURL,
	})
}

// WithRegistrationHandler sets the self-service registration handler for the server.
func WithRegistrationHandler(handler *RegistrationHandler) ServerOption {
	return func(s *Server) {
		s.registrationHandler = handler
	}
}

// isAlreadyExistsError checks if an error indicates a resource already exists.
func isAlreadyExistsError(err error) bool {
	return strings.Contains(err.Error(), "already exists") ||
		strings.Contains(err.Error(), "AlreadyExists")
}
