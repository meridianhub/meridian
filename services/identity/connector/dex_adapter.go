// Package connector provides the Dex integration adapter for the Meridian
// identity connector.
//
// This adapter bridges the Meridian identity connector to the Dex sidecar's
// PasswordConnector interface. When building a custom Dex binary, this
// adapter's types map 1:1 to Dex's connector.Identity and
// connector.PasswordConnector.
//
// DexPasswordConnector wraps the internal Connector to implement the
// PasswordConnector interface. It handles:
//
//   - Tenant context propagation: extracts tenant slug from the username field
//     (format "tenant:<slug>/<email>") and resolves it to a TenantID via the
//     tenant repository before delegating to the internal connector.
//   - Identity mapping: converts the internal connector.Identity to a
//     DexIdentity with tenant context in ConnectorData and Groups.
//   - Custom claims: encodes tenant_id in ConnectorData and adds a
//     "tenant:<tenant_id>" entry to Groups for downstream JWT enrichment.
package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	tenantdomain "github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Scopes mirrors Dex's connector.Scopes struct. It describes the set of
// scopes requested by the OAuth2 client. This local definition avoids
// importing the Dex Go module (which runs as an external sidecar).
type Scopes struct {
	OfflineAccess bool
	Groups        bool
}

// DexIdentity mirrors Dex's connector.Identity struct. It represents the
// authenticated user identity returned by the connector to Dex. This local
// definition maps 1:1 to Dex's type without requiring a Go module dependency.
type DexIdentity struct {
	UserID            string
	Username          string
	PreferredUsername string
	Email             string
	EmailVerified     bool
	Groups            []string
	ConnectorData     []byte
}

// TenantResolver looks up a tenant by slug and returns its TenantID.
// This is satisfied by tenantdomain.TenantRepository but defined as a
// narrow interface to avoid coupling the adapter to the full repository.
type TenantResolver interface {
	GetBySlug(ctx context.Context, slug string) (*tenantdomain.Tenant, error)
}

// DexPasswordConnector adapts the internal Meridian Connector to satisfy Dex's
// connector.PasswordConnector interface.
//
// Tenant context is extracted from the username field. When the username
// contains a "tenant:<slug>/" prefix, the adapter strips the prefix, resolves
// the slug to a TenantID, and injects it into the context before calling the
// underlying connector. Without the prefix, the adapter returns an
// authentication failure.
type DexPasswordConnector struct {
	connector      *Connector
	tenantResolver TenantResolver
	logger         *slog.Logger
}

// Sentinel errors for DexPasswordConnector construction and operation.
var (
	// ErrTenantResolverNil is returned when a nil tenant resolver is provided.
	ErrTenantResolverNil = errors.New("connector: tenant resolver must not be nil")

	// ErrConnectorNil is returned when a nil connector is provided.
	ErrConnectorNil = errors.New("connector: connector must not be nil")

	// ErrMissingTenantPrefix is returned when the username lacks the "tenant:" prefix.
	ErrMissingTenantPrefix = errors.New("username must start with 'tenant:' prefix")

	// ErrMissingSeparator is returned when the username lacks the "/" separator.
	ErrMissingSeparator = errors.New("username must contain '/' separator between slug and email")

	// ErrEmptySlug is returned when the tenant slug portion is empty.
	ErrEmptySlug = errors.New("tenant slug cannot be empty")

	// ErrEmptyEmail is returned when the email portion is empty.
	ErrEmptyEmail = errors.New("email cannot be empty")

	// ErrMissingTenantInConnectorData is returned on refresh when ConnectorData
	// does not contain a tenant_id.
	ErrMissingTenantInConnectorData = errors.New("dex adapter: missing tenant_id in connector data")

	// ErrUserNoLongerValid is returned on refresh when the user cannot be
	// re-authenticated.
	ErrUserNoLongerValid = errors.New("dex adapter: user no longer valid")
)

// NewDexPasswordConnector creates a DexPasswordConnector that wraps the given
// internal connector and uses the tenant resolver for slug-to-ID mapping.
func NewDexPasswordConnector(c *Connector, resolver TenantResolver, logger *slog.Logger) (*DexPasswordConnector, error) {
	if c == nil {
		return nil, ErrConnectorNil
	}
	if resolver == nil {
		return nil, ErrTenantResolverNil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DexPasswordConnector{
		connector:      c,
		tenantResolver: resolver,
		logger:         logger,
	}, nil
}

// Prompt returns the label displayed on the Dex login form.
func (d *DexPasswordConnector) Prompt() string {
	return "Email"
}

// connectorData is serialized into connector.Identity.ConnectorData to carry
// tenant context through Dex's token pipeline.
type connectorData struct {
	TenantID string `json:"tenant_id"`
}

// tenantGroupPrefix is the prefix used for encoding tenant ID in the Groups
// claim. Dex propagates Groups to the JWT, allowing downstream services to
// extract the tenant ID from the "groups" claim.
const tenantGroupPrefix = "tenant:"

// Login validates credentials via the internal connector after resolving tenant
// context from the username field.
//
// Username format: "tenant:<slug>/<email>"
//
//	Example: "tenant:volterra/admin@volterra.energy"
//
// The adapter:
//  1. Parses the tenant slug and email from the username
//  2. Resolves the slug to a TenantID via the tenant repository
//  3. Injects the TenantID into the context
//  4. Delegates to the internal Connector.Login
//  5. Maps the result to a Dex connector.Identity with tenant context
func (d *DexPasswordConnector) Login(ctx context.Context, s Scopes, username, password string) (DexIdentity, bool, error) {
	slug, email, err := parseTenantUsername(username)
	if err != nil {
		d.logger.InfoContext(ctx, "dex adapter: invalid username format",
			"error", err)
		return DexIdentity{}, false, nil
	}

	// Resolve tenant slug to TenantID.
	tenantEntity, err := d.tenantResolver.GetBySlug(ctx, slug)
	if err != nil {
		// Check both domain and persistence not-found errors because the
		// TenantResolver implementation may return either depending on the layer.
		if errors.Is(err, tenantdomain.ErrNotFound) || errors.Is(err, tenantpersistence.ErrTenantNotFound) {
			d.logger.InfoContext(ctx, "dex adapter: tenant not found",
				"slug", slug)
			return DexIdentity{}, false, nil
		}
		d.logger.ErrorContext(ctx, "dex adapter: failed to resolve tenant",
			"slug", slug,
			"error", err)
		return DexIdentity{}, false, fmt.Errorf("dex adapter: resolve tenant: %w", err)
	}

	tenantID := tenantEntity.ID
	ctx = tenant.WithTenant(ctx, tenantID)

	// Convert Dex scopes to string slice for internal connector.
	scopes := dexScopesToStrings(s)

	// Delegate to internal connector.
	identity, valid, err := d.connector.Login(ctx, scopes, email, password)
	if err != nil {
		return DexIdentity{}, false, err
	}
	if !valid {
		return DexIdentity{}, false, nil
	}

	// Build Dex identity with tenant context.
	dexIdentity := toDexIdentity(identity, tenantID)

	d.logger.InfoContext(ctx, "dex adapter: login successful",
		"tenant_id", tenantID)

	return dexIdentity, true, nil
}

// Refresh updates the identity on token refresh. It re-resolves tenant context
// from the ConnectorData stored during the initial login.
func (d *DexPasswordConnector) Refresh(ctx context.Context, _ Scopes, identity DexIdentity) (DexIdentity, error) {
	// Extract tenant ID from stored connector data.
	var cd connectorData
	if len(identity.ConnectorData) > 0 {
		if err := json.Unmarshal(identity.ConnectorData, &cd); err != nil {
			return DexIdentity{}, fmt.Errorf("dex adapter: unmarshal connector data: %w", err)
		}
	}

	if cd.TenantID == "" {
		return DexIdentity{}, ErrMissingTenantInConnectorData
	}

	tenantID, err := tenant.NewTenantID(cd.TenantID)
	if err != nil {
		return DexIdentity{}, fmt.Errorf("dex adapter: invalid tenant_id in connector data: %w", err)
	}

	ctx = tenant.WithTenant(ctx, tenantID)

	// Re-resolve identity to pick up any changes (updated roles, status).
	// Uses Resolve (not Login) because refresh does not have the user's password.
	updatedIdentity, valid, err := d.connector.Resolve(ctx, identity.Email)
	if err != nil || !valid {
		// On refresh, if user is no longer valid, return error to invalidate token.
		if err == nil {
			err = ErrUserNoLongerValid
		}
		return DexIdentity{}, err
	}

	return toDexIdentity(updatedIdentity, tenantID), nil
}

// parseTenantUsername extracts the tenant slug and email from a username
// in the format "tenant:<slug>/<email>".
func parseTenantUsername(username string) (slug, email string, err error) {
	if !strings.HasPrefix(username, tenantGroupPrefix) {
		return "", "", ErrMissingTenantPrefix
	}

	// Remove "tenant:" prefix.
	rest := strings.TrimPrefix(username, tenantGroupPrefix)

	// Split on first "/" to separate slug from email.
	idx := strings.Index(rest, "/")
	if idx < 0 {
		return "", "", ErrMissingSeparator
	}

	slug = rest[:idx]
	email = rest[idx+1:]

	if slug == "" {
		return "", "", ErrEmptySlug
	}
	if email == "" {
		return "", "", ErrEmptyEmail
	}

	return slug, email, nil
}

// toDexIdentity converts an internal connector.Identity to a Dex
// connector.Identity, encoding tenant context in ConnectorData and Groups.
func toDexIdentity(identity Identity, tenantID tenant.TenantID) DexIdentity {
	// Encode tenant ID in ConnectorData for refresh token support.
	cd := connectorData{TenantID: tenantID.String()}
	cdBytes, _ := json.Marshal(cd) // connectorData is simple; marshal won't fail.

	// Add tenant ID to groups so it appears in the JWT "groups" claim.
	groups := make([]string, 0, len(identity.Groups)+1)
	groups = append(groups, tenantGroupPrefix+tenantID.String())
	groups = append(groups, identity.Groups...)

	return DexIdentity{
		UserID:        identity.UserID,
		Username:      identity.Username,
		Email:         identity.Email,
		EmailVerified: identity.EmailVerified,
		Groups:        groups,
		ConnectorData: cdBytes,
	}
}

// dexScopesToStrings converts Dex Scopes to a string slice for the internal
// connector. Currently the internal connector ignores scopes, but this
// maintains interface compatibility.
func dexScopesToStrings(s Scopes) []string {
	var scopes []string
	if s.OfflineAccess {
		scopes = append(scopes, "offline_access")
	}
	if s.Groups {
		scopes = append(scopes, "groups")
	}
	return scopes
}
