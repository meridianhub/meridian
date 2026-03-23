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
	ErrRegistrationLoggerRequired   = errors.New("registration handler: logger is required")
	ErrRegistrationTenantRequired   = errors.New("registration handler: tenant creator is required")
	ErrRegistrationIdentityRequired = errors.New("registration handler: identity repository is required")

	errInvalidRequestBody       = errors.New("invalid request body")
	errMissingRequiredFields    = errors.New("slug, email, and password are required")
	errInvalidSlug              = errors.New("invalid slug")
	errPasswordPolicyViolation  = errors.New("password policy violation")
	errSlugCheckFailed          = errors.New("failed to check slug availability")
	errSlugTaken                = errors.New("slug is already taken")
	errTenantAlreadyExists      = errors.New("tenant already exists")
	errTenantCreationFailed     = errors.New("failed to create tenant")
	errIdentityCreationFailed   = errors.New("failed to create identity")
	errInitTenantIdentityFailed = errors.New("failed to initialize tenant identity")
	errEmailAlreadyRegistered   = errors.New("email already registered in this tenant")
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

	clientIP := ClientIP(r)
	if !h.rateLimiter.Allow(clientIP) {
		h.logger.WarnContext(r.Context(), "registration rate limited",
			"client_ip", clientIP)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "too many registration attempts, please try again later",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	req, err := h.parseAndValidateRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ctx := r.Context()

	status, resp, err := h.executeRegistration(ctx, req)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	h.logger.InfoContext(ctx, "registration: tenant and admin identity created",
		"tenant_id", resp.TenantID,
		"slug", req.Slug,
		"client_ip", clientIP)

	writeJSON(w, http.StatusCreated, resp)
}

// parseAndValidateRequest decodes and validates the registration request body.
func (h *RegistrationHandler) parseAndValidateRequest(r *http.Request) (*registrationRequest, error) {
	var req registrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, errInvalidRequestBody
	}

	if req.Slug == "" || req.Email == "" || req.Password == "" {
		return nil, errMissingRequiredFields
	}

	if err := tenantdomain.ValidateSlug(req.Slug); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidSlug, err)
	}

	if err := credentials.ValidatePasswordPolicy(req.Password); err != nil {
		return nil, fmt.Errorf("%w: %v", errPasswordPolicyViolation, err)
	}

	return &req, nil
}

// executeRegistration performs tenant creation and identity provisioning.
// Returns (httpStatus, response, error). On success error is nil.
func (h *RegistrationHandler) executeRegistration(ctx context.Context, req *registrationRequest) (int, *registrationResponse, error) {
	available, err := h.tenantCreator.IsSlugAvailable(ctx, req.Slug)
	if err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to check slug availability",
			"slug", req.Slug, "error", err)
		return http.StatusInternalServerError, nil, errSlugCheckFailed
	}
	if !available {
		return http.StatusConflict, nil, errSlugTaken
	}

	tenantID := strings.ReplaceAll(req.Slug, "-", "_")
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Slug
	}

	createdTenantID, err := h.tenantCreator.CreateTenant(ctx, tenantID, req.Slug, displayName)
	if err != nil {
		if isAlreadyExistsError(err) {
			return http.StatusConflict, nil, errTenantAlreadyExists
		}
		h.logger.ErrorContext(ctx, "registration: failed to create tenant",
			"tenant_id", tenantID, "error", err)
		return http.StatusInternalServerError, nil, errTenantCreationFailed
	}

	if err := h.provisionAdminIdentity(ctx, createdTenantID, req.Email, req.Password); err != nil {
		return err.status, nil, err
	}

	loginURL := fmt.Sprintf("https://%s.%s/login", req.Slug, h.baseDomain)
	if h.baseDomain == "" {
		loginURL = fmt.Sprintf("/login?tenant=%s", req.Slug)
	}

	return http.StatusCreated, &registrationResponse{
		TenantID: createdTenantID,
		LoginURL: loginURL,
	}, nil
}

// registrationError is an error with an associated HTTP status code.
type registrationError struct {
	status int
	inner  error
}

func (e *registrationError) Error() string { return e.inner.Error() }
func (e *registrationError) Unwrap() error { return e.inner }

func newRegistrationError(status int, inner error) *registrationError {
	return &registrationError{status: status, inner: inner}
}

// provisionAdminIdentity creates the initial admin identity within the new tenant's scope.
func (h *RegistrationHandler) provisionAdminIdentity(ctx context.Context, tenantIDStr, email, password string) *registrationError {
	tid, err := tenant.NewTenantID(tenantIDStr)
	if err != nil {
		h.logger.ErrorContext(ctx, "registration: invalid tenant ID from creation",
			"tenant_id", tenantIDStr, "error", err)
		return newRegistrationError(http.StatusInternalServerError, errInitTenantIdentityFailed)
	}

	tenantCtx := tenant.WithTenant(ctx, tid)

	identity, err := identitydomain.NewIdentity(email)
	if err != nil {
		return newRegistrationError(http.StatusBadRequest, fmt.Errorf("%w: %w", errIdentityCreationFailed, err))
	}

	hash, err := credentials.HashPassword(password)
	if err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to hash password", "error", err)
		return newRegistrationError(http.StatusInternalServerError, errIdentityCreationFailed)
	}

	if err := identity.SetPassword(hash); err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to set password", "error", err)
		return newRegistrationError(http.StatusInternalServerError, errIdentityCreationFailed)
	}

	if err := identity.Activate(); err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to activate identity", "error", err)
		return newRegistrationError(http.StatusInternalServerError, errIdentityCreationFailed)
	}

	now := time.Now()
	ra := identitydomain.ReconstructRoleAssignment(
		uuid.New(),
		identity.ID(),
		identity.ID(),
		identitydomain.RoleTenantOwner,
		nil, nil, nil,
		now, now,
	)

	if err := h.identityRepo.SaveIdentityWithRoles(tenantCtx, identity, []*identitydomain.RoleAssignment{ra}); err != nil {
		if errors.Is(err, identitydomain.ErrEmailAlreadyExists) {
			return newRegistrationError(http.StatusConflict, errEmailAlreadyRegistered)
		}
		h.logger.ErrorContext(ctx, "registration: failed to save identity",
			"tenant_id", tenantIDStr, "error", err)
		return newRegistrationError(http.StatusInternalServerError, errIdentityCreationFailed)
	}

	return nil
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
