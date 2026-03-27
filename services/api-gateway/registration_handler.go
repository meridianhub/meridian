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
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	errSlugTaken                = errors.New("slug is already taken")
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
	// DeleteTenant removes a tenant. Used as compensation when identity provisioning
	// fails after tenant creation, preventing orphaned tenants.
	DeleteTenant(ctx context.Context, tenantID string) error
}

// SlugChecker abstracts slug availability checks for the registration handler.
type SlugChecker interface {
	// IsSlugAvailable returns true if the slug is not in use.
	IsSlugAvailable(ctx context.Context, slug string) (bool, error)
}

// RegistrationHandler handles self-service tenant registration.
// POST /api/v1/register - creates a tenant and an initial admin identity.
// GET /api/v1/slugs/{slug}/available - checks slug availability.
type RegistrationHandler struct {
	tenantCreator             TenantCreator
	slugChecker               SlugChecker
	identityRepo              identitydomain.Repository
	outboxRepo                email.OutboxRepository
	rateLimiter               *RegistrationRateLimiter
	baseDomain                string
	emailVerificationRequired bool
	logger                    *slog.Logger
}

// RegistrationHandlerConfig holds dependencies for creating a RegistrationHandler.
type RegistrationHandlerConfig struct {
	TenantCreator              TenantCreator
	SlugChecker                SlugChecker
	IdentityRepo               identitydomain.Repository
	OutboxRepo                 email.OutboxRepository
	RateLimiter                *RegistrationRateLimiter
	BaseDomain                 string
	EmailVerificationRequired  bool
	Logger                     *slog.Logger
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
		rl = NewRegistrationRateLimiter(20)
	}
	return &RegistrationHandler{
		tenantCreator:             cfg.TenantCreator,
		slugChecker:               cfg.SlugChecker,
		identityRepo:              cfg.IdentityRepo,
		outboxRepo:                cfg.OutboxRepo,
		rateLimiter:               rl,
		baseDomain:                cfg.BaseDomain,
		emailVerificationRequired: cfg.EmailVerificationRequired,
		logger:                    cfg.Logger,
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
	TenantID             string `json:"tenant_id"`
	LoginURL             string `json:"login_url"`
	VerificationRequired bool   `json:"verification_required"`
}

// HandleRegister handles POST /api/v1/register.
func (h *RegistrationHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := getClientIP(r)
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
		return nil, fmt.Errorf("%w: %w", errInvalidSlug, err)
	}

	if err := credentials.ValidatePasswordPolicy(req.Password); err != nil {
		return nil, fmt.Errorf("%w: %w", errPasswordPolicyViolation, err)
	}

	return &req, nil
}

// executeRegistration performs tenant creation and identity provisioning.
// Returns (httpStatus, response, error). On success error is nil.
func (h *RegistrationHandler) executeRegistration(ctx context.Context, req *registrationRequest) (int, *registrationResponse, error) {
	tenantID := strings.ReplaceAll(req.Slug, "-", "_")
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Slug
	}

	createdTenantID, err := h.tenantCreator.CreateTenant(ctx, tenantID, req.Slug, displayName)
	if err != nil {
		if isAlreadyExistsError(err) {
			return http.StatusConflict, nil, errSlugTaken
		}
		h.logger.ErrorContext(ctx, "registration: failed to create tenant",
			"tenant_id", tenantID, "error", err)
		return http.StatusInternalServerError, nil, errTenantCreationFailed
	}

	regErr := h.provisionAdminIdentity(ctx, createdTenantID, req.Email, req.Password)
	if regErr != nil {
		// Compensate: delete orphaned tenant to allow the user to retry.
		if delErr := h.tenantCreator.DeleteTenant(ctx, createdTenantID); delErr != nil {
			h.logger.ErrorContext(ctx, "registration: failed to compensate (delete tenant)",
				"tenant_id", createdTenantID, "error", delErr)
		}
		return regErr.status, nil, regErr
	}

	loginURL := fmt.Sprintf("https://%s.%s/login", req.Slug, h.baseDomain)
	if h.baseDomain == "" {
		loginURL = fmt.Sprintf("/login?tenant=%s", req.Slug)
	}

	return http.StatusCreated, &registrationResponse{
		TenantID:             createdTenantID,
		LoginURL:             loginURL,
		VerificationRequired: h.emailVerificationRequired,
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

	var identity *identitydomain.Identity
	if h.emailVerificationRequired {
		identity, err = identitydomain.NewSelfRegisteredIdentity(tid, email, true)
	} else {
		identity, err = identitydomain.NewIdentity(tid, email)
	}
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

	if !h.emailVerificationRequired {
		if err := identity.Activate(); err != nil {
			h.logger.ErrorContext(ctx, "registration: failed to activate identity", "error", err)
			return newRegistrationError(http.StatusInternalServerError, errIdentityCreationFailed)
		}
	}

	now := time.Now()
	// ReconstructRoleAssignment is used instead of NewRoleAssignment because
	// this is a system-level bootstrap operation (no granting identity exists yet).
	// This follows the same pattern as identity/bootstrap/bootstrap.go.
	ra := identitydomain.ReconstructRoleAssignment(
		uuid.New(),
		tid,
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

	// Queue verification email when verification is required.
	if h.emailVerificationRequired && h.outboxRepo != nil {
		h.queueRegistrationVerificationEmail(tenantCtx, tenantIDStr, identity)
	}

	return nil
}

// queueRegistrationVerificationEmail creates a verification token and queues the email.
func (h *RegistrationHandler) queueRegistrationVerificationEmail(ctx context.Context, tenantIDStr string, identity *identitydomain.Identity) {
	vtoken, plaintext, err := identitydomain.NewVerificationToken(tenantIDStr, identity.ID())
	if err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to create verification token", "error", err)
		return
	}

	if err := h.identityRepo.SaveVerificationToken(ctx, vtoken); err != nil {
		h.logger.ErrorContext(ctx, "registration: failed to save verification token", "error", err)
		return
	}

	verificationLink := fmt.Sprintf("https://%s/verify-email?token=%s", h.baseDomain, plaintext)
	if h.baseDomain == "" {
		verificationLink = "/verify-email?token=" + plaintext
	}

	entry := &email.OutboxEntry{
		ID:             uuid.New(),
		TenantID:       tenantIDStr,
		IdempotencyKey: "verify-email-" + identity.ID().String(),
		ToAddresses:    []string{identity.Email()},
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
		h.logger.ErrorContext(ctx, "registration: failed to enqueue verification email", "error", err)
	}
}

// HandleSlugAvailable handles GET /api/v1/slugs/{slug}/available.
// Returns {"available": true/false} with optional validation errors.
// Not rate-limited: this is a read-only check that the frontend fires on keystroke.
// Format validation prevents brute-force enumeration (slugs must be 3+ chars, lowercase).
func (h *RegistrationHandler) HandleSlugAvailable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug is required"})
		return
	}

	if err := tenantdomain.ValidateSlug(slug); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"available": false,
			"reason":    fmt.Sprintf("invalid slug: %v", err),
		})
		return
	}

	if h.slugChecker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "slug availability check not configured",
		})
		return
	}

	available, err := h.slugChecker.IsSlugAvailable(r.Context(), slug)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "slug availability check failed",
			"slug", slug, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to check slug availability",
		})
		return
	}

	resp := map[string]interface{}{"available": available}
	if !available {
		resp["reason"] = "slug is already taken"
	}
	writeJSON(w, http.StatusOK, resp)
}

// WithRegistrationHandler sets the self-service registration handler for the server.
func WithRegistrationHandler(handler *RegistrationHandler) ServerOption {
	return func(s *Server) {
		s.registrationHandler = handler
	}
}

// isAlreadyExistsError checks if an error indicates a resource already exists
// using the gRPC status code rather than brittle string matching.
func isAlreadyExistsError(err error) bool {
	if s, ok := status.FromError(err); ok {
		return s.Code() == codes.AlreadyExists
	}
	return false
}
