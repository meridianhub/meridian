// Package connector implements a local Dex password connector that validates
// credentials directly against the identity domain layer without a network hop.
//
// Dex supports pluggable connectors via the connector.PasswordConnector interface.
// Since Meridian runs Dex in the same process (or tightly coupled), this connector
// bypasses HTTP/gRPC overhead by calling the domain repository directly.
//
// The connector:
//   - Resolves the tenant from context metadata (set by the gateway from subdomain)
//   - Looks up the identity by email within that tenant scope
//   - Validates the password using bcrypt via the credentials package
//   - Checks account status (locked, suspended, pending invite → reject)
//   - Queries active role assignments and maps them to Dex groups
//   - Returns a connector.Identity with groups populated for JWT claim injection
package connector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Identity represents the result of a successful authentication, compatible with
// Dex's connector.Identity shape. Groups are populated with the identity's active roles
// and are injected into the JWT as the "groups" claim by Dex.
type Identity struct {
	// UserID is the stable identifier for the user (UUID string).
	UserID string
	// Username is the display name, defaulting to email if not set.
	Username string
	// Email is the verified email address.
	Email string
	// EmailVerified indicates whether the email has been verified.
	EmailVerified bool
	// Groups contains active role assignments, used to populate JWT group claims.
	Groups []string
	// ConnectorData is opaque bytes stored by Dex for refresh token support.
	ConnectorData []byte
}

// LoginResult is returned by Login to convey both success state and the identity.
type LoginResult struct {
	Identity Identity
	// Valid is true when authentication succeeded.
	Valid bool
}

// PasswordConnector validates username/password credentials.
// This interface mirrors Dex's connector.PasswordConnector to keep the implementation
// decoupled from the Dex library (which is not a declared Go module dependency).
type PasswordConnector interface {
	// Login validates credentials and returns the identity on success.
	// valid is false when credentials are incorrect without an underlying error.
	Login(ctx context.Context, scopes []string, username, password string) (identity Identity, valid bool, err error)
}

// ErrRepositoryNil is returned by New when a nil repository is provided.
var ErrRepositoryNil = errors.New("connector: repository must not be nil")

// OutboxWriter queues an email for delivery. A narrow interface so the connector
// does not depend on the full email.OutboxRepository.
type OutboxWriter interface {
	Enqueue(ctx context.Context, entry *email.OutboxEntry) error
}

// Option configures a Connector.
type Option func(*Connector)

// WithEmailOutbox sets the outbox writer used to queue notification emails.
// If not provided (or nil), email notifications are silently skipped.
func WithEmailOutbox(outbox OutboxWriter) Option {
	return func(c *Connector) {
		c.emailOutbox = outbox
	}
}

// Connector is the local implementation of PasswordConnector.
// It performs credential validation and role resolution directly against the
// identity domain repository, avoiding any network hop.
type Connector struct {
	repo        domain.Repository
	logger      *slog.Logger
	emailOutbox OutboxWriter
}

// New creates a Connector with the given repository. If logger is nil a default
// JSON logger writing to stdout is used. Optional Option functions may be
// provided to configure additional behavior such as email notifications.
func New(repo domain.Repository, logger *slog.Logger, opts ...Option) (*Connector, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	c := &Connector{repo: repo, logger: logger}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c, nil
}

// Login validates the supplied username (email) and password against the identity
// domain within the tenant derived from ctx.
//
// Returns:
//   - (identity, true, nil) on success
//   - (zero, false, nil) when credentials are simply wrong (no programming error)
//   - (zero, false, err) only for unexpected infrastructure errors
func (c *Connector) Login(ctx context.Context, _ []string, username, password string) (Identity, bool, error) {
	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		c.logger.ErrorContext(ctx, "connector: tenant context missing during login",
			"error", err)
		return Identity{}, false, fmt.Errorf("connector: %w", err)
	}

	identity, err := c.repo.FindByEmail(ctx, username)
	if err != nil {
		if errors.Is(err, domain.ErrIdentityNotFound) {
			c.logger.InfoContext(ctx, "connector: identity not found",
				"tenant_id", tenantID)
			return Identity{}, false, nil
		}
		c.logger.ErrorContext(ctx, "connector: repository error looking up identity",
			"tenant_id", tenantID,
			"username", username,
			"error", err)
		return Identity{}, false, fmt.Errorf("connector: lookup identity: %w", err)
	}

	// Reject non-active accounts before any password check.
	if rejected, rejErr := c.checkAccountStatus(ctx, identity, tenantID); rejected {
		return Identity{}, false, rejErr
	}

	if err := credentials.ValidatePassword(password, identity.PasswordHash()); err != nil {
		c.handleFailedLogin(ctx, identity, tenantID)
		return Identity{}, false, nil
	}

	// Record successful login; best-effort.
	_ = identity.RecordLoginAttempt(true)
	if saveErr := c.repo.Save(ctx, identity); saveErr != nil {
		c.logger.ErrorContext(ctx, "connector: failed to persist successful login",
			"identity_id", identity.ID(),
			"error", saveErr)
	}

	return c.buildLoginIdentity(ctx, identity, tenantID)
}

// checkAccountStatus rejects non-active accounts. Returns (true, error) if rejected.
func (c *Connector) checkAccountStatus(ctx context.Context, identity *domain.Identity, tenantID tenant.TenantID) (bool, error) {
	switch identity.Status() {
	case domain.IdentityStatusLocked:
		c.logger.InfoContext(ctx, "connector: login rejected - account locked",
			"tenant_id", tenantID, "identity_id", identity.ID())
		return true, nil
	case domain.IdentityStatusSuspended:
		c.logger.InfoContext(ctx, "connector: login rejected - account suspended",
			"tenant_id", tenantID, "identity_id", identity.ID())
		return true, nil
	case domain.IdentityStatusPendingInvite:
		c.logger.InfoContext(ctx, "connector: login rejected - account not yet activated",
			"tenant_id", tenantID, "identity_id", identity.ID())
		return true, nil
	case domain.IdentityStatusPendingVerification:
		c.logger.InfoContext(ctx, "connector: login rejected - email not yet verified",
			"tenant_id", tenantID, "identity_id", identity.ID())
		return true, domain.ErrEmailNotVerified
	case domain.IdentityStatusActive:
		return false, nil
	default:
		c.logger.WarnContext(ctx, "connector: login rejected - unknown account status",
			"tenant_id", tenantID, "identity_id", identity.ID(), "status", identity.Status())
		return true, nil
	}
}

// handleFailedLogin records a failed login attempt and queues a lockout email if the account was just locked.
func (c *Connector) handleFailedLogin(ctx context.Context, identity *domain.Identity, tenantID tenant.TenantID) {
	_ = identity.RecordLoginAttempt(false)
	justLocked := identity.IsLocked()
	if saveErr := c.repo.Save(ctx, identity); saveErr != nil {
		c.logger.ErrorContext(ctx, "connector: failed to persist failed login attempt",
			"identity_id", identity.ID(), "error", saveErr)
	} else if justLocked {
		c.queueLockoutEmail(ctx, identity, tenantID)
	}
	c.logger.InfoContext(ctx, "connector: invalid password",
		"tenant_id", tenantID, "identity_id", identity.ID())
}

// buildLoginIdentity resolves roles and constructs the login Identity response.
func (c *Connector) buildLoginIdentity(ctx context.Context, identity *domain.Identity, tenantID tenant.TenantID) (Identity, bool, error) {
	groups, err := c.activeRoles(ctx, identity)
	if err != nil {
		c.logger.ErrorContext(ctx, "connector: failed to load role assignments",
			"identity_id", identity.ID(), "error", err)
		groups = []string{}
	}

	connIdentity := Identity{
		UserID:        identity.ID().String(),
		Username:      identity.Email(),
		Email:         identity.Email(),
		EmailVerified: true,
		Groups:        groups,
	}

	c.logger.InfoContext(ctx, "connector: login successful",
		"tenant_id", tenantID, "identity_id", identity.ID(), "roles", groups)

	return connIdentity, true, nil
}

// queueLockoutEmail enqueues an account-lockout notification for the given identity.
// Best-effort: errors are logged but not surfaced to the caller.
// ErrDuplicateIdempotency is silently ignored to handle concurrent lockout attempts.
func (c *Connector) queueLockoutEmail(ctx context.Context, identity *domain.Identity, tenantID tenant.TenantID) {
	if c.emailOutbox == nil {
		return
	}
	now := time.Now().UTC()
	// Include date in idempotency key so a re-lock after admin unlock on a
	// different day produces a new notification.
	entry := &email.OutboxEntry{
		IdempotencyKey: "account-lockout:" + identity.ID().String() + ":" + now.Format("2006-01-02"),
		ToAddresses:    []string{identity.Email()},
		FromAddress:    "noreply@meridianhub.cloud",
		Subject:        "Your account has been locked",
		TemplateName:   "account-lockout",
		TemplateData: map[string]any{
			"TenantName":   string(tenantID),
			"SupportEmail": "support@meridianhub.cloud",
			"LockoutTime":  now.Format(time.RFC3339),
		},
	}
	if err := c.emailOutbox.Enqueue(ctx, entry); err != nil {
		if errors.Is(err, email.ErrDuplicateIdempotency) {
			c.logger.InfoContext(ctx, "connector: lockout email already queued (idempotent)",
				"identity_id", identity.ID())
			return
		}
		c.logger.ErrorContext(ctx, "connector: failed to queue lockout email",
			"identity_id", identity.ID(),
			"error", err)
	}
}

// activeRoles returns the string role names for all non-revoked, non-expired
// role assignments associated with the given identity.
func (c *Connector) activeRoles(ctx context.Context, identity *domain.Identity) ([]string, error) {
	assignments, err := c.repo.FindRoleAssignments(ctx, identity.ID())
	if err != nil {
		return nil, fmt.Errorf("find role assignments: %w", err)
	}

	roles := make([]string, 0, len(assignments))
	for _, a := range assignments {
		if a.IsActive() {
			roles = append(roles, string(a.Role()))
		}
	}
	return roles, nil
}

// Resolve looks up an identity by email without password validation.
// It returns the same Identity struct as Login but skips credential checks.
// Used by the Dex adapter during token refresh to re-resolve user attributes
// (roles, status) without requiring the password.
//
// Returns:
//   - (identity, true, nil) if the identity exists and is active
//   - (zero, false, nil) if the identity is not found, locked, suspended, or pending
//   - (zero, false, err) for unexpected infrastructure errors
func (c *Connector) Resolve(ctx context.Context, email string) (Identity, bool, error) {
	tenantID, err := tenant.RequireFromContext(ctx)
	if err != nil {
		c.logger.ErrorContext(ctx, "connector: tenant context missing during resolve",
			"error", err)
		return Identity{}, false, fmt.Errorf("connector: %w", err)
	}

	identity, err := c.repo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrIdentityNotFound) {
			c.logger.InfoContext(ctx, "connector: identity not found during resolve",
				"tenant_id", tenantID)
			return Identity{}, false, nil
		}
		c.logger.ErrorContext(ctx, "connector: repository error during resolve",
			"tenant_id", tenantID,
			"email", email,
			"error", err)
		return Identity{}, false, fmt.Errorf("connector: resolve identity: %w", err)
	}

	// Reject non-active accounts.
	if identity.Status() != domain.IdentityStatusActive {
		c.logger.InfoContext(ctx, "connector: resolve rejected — account not active",
			"tenant_id", tenantID,
			"identity_id", identity.ID(),
			"status", identity.Status())
		return Identity{}, false, nil
	}

	groups, err := c.activeRoles(ctx, identity)
	if err != nil {
		// During refresh, failing to load roles is an error — returning a token
		// with missing roles could grant incorrect permissions.
		c.logger.ErrorContext(ctx, "connector: failed to load role assignments during resolve",
			"identity_id", identity.ID(),
			"error", err)
		return Identity{}, false, fmt.Errorf("connector: resolve roles: %w", err)
	}

	connIdentity := Identity{
		UserID:        identity.ID().String(),
		Username:      identity.Email(),
		Email:         identity.Email(),
		EmailVerified: true,
		Groups:        groups,
	}

	c.logger.InfoContext(ctx, "connector: resolve successful",
		"tenant_id", tenantID,
		"identity_id", identity.ID(),
		"roles", groups)

	return connIdentity, true, nil
}
