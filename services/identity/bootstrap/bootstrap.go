// Package bootstrap provides the platform admin bootstrap process for the identity service.
//
// On first boot, it creates the initial platform admin identity in the meridian_master
// tenant using credentials from environment variables. The process is idempotent —
// safe to call on every boot.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// MasterTenantID is the well-known tenant ID for the master/platform tenant.
const MasterTenantID = "meridian_master"

// ErrNilRepository is returned when a nil repository is passed to Run.
var ErrNilRepository = errors.New("bootstrap: repository must not be nil")

// platformAdminRoles are the roles assigned to the bootstrapped platform admin.
var platformAdminRoles = []auth.Role{
	auth.RolePlatformAdmin,
	auth.RoleSuperAdmin,
	auth.RoleTenantOwner,
}

// Run creates the initial platform admin identity in the meridian_master tenant
// from environment variables on first boot.
//
// Required environment variables:
//   - PLATFORM_ADMIN_EMAIL: Email address for the platform admin
//   - PLATFORM_ADMIN_PASSWORD: Initial password for the platform admin
//
// If either variable is empty, the function logs an info message and returns nil.
// The function is idempotent:
//   - If an admin already exists, any missing roles are reconciled atomically.
//   - Identity creation and all role assignments are committed in a single transaction.
func Run(ctx context.Context, repo domain.Repository) error {
	if repo == nil {
		return ErrNilRepository
	}

	email := os.Getenv("PLATFORM_ADMIN_EMAIL")
	password := os.Getenv("PLATFORM_ADMIN_PASSWORD")

	if email == "" || password == "" {
		slog.InfoContext(ctx, "platform admin bootstrap skipped: PLATFORM_ADMIN_EMAIL or PLATFORM_ADMIN_PASSWORD not set")
		return nil
	}

	masterTenantID, err := tenant.NewTenantID(MasterTenantID)
	if err != nil {
		return fmt.Errorf("invalid master tenant ID: %w", err)
	}
	masterCtx := tenant.WithTenant(ctx, masterTenantID)

	// Check if an admin already exists.
	existing, err := repo.FindByEmail(masterCtx, email)
	if err != nil && !errors.Is(err, domain.ErrIdentityNotFound) {
		return fmt.Errorf("checking for existing platform admin: %w", err)
	}

	if existing != nil {
		// Admin already exists — reconcile any missing roles atomically.
		return reconcileRoles(masterCtx, repo, existing)
	}

	// Hash the password before opening the transaction.
	hash, err := credentials.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing platform admin password: %w", err)
	}

	// Build domain objects before the transaction.
	masterTenant := tenant.MustNewTenantID(MasterTenantID)
	identity, err := domain.NewIdentity(masterTenant, email)
	if err != nil {
		return fmt.Errorf("creating platform admin identity: %w", err)
	}
	if err := identity.SetPassword(hash); err != nil {
		return fmt.Errorf("setting platform admin password: %w", err)
	}
	if err := identity.Activate(); err != nil {
		return fmt.Errorf("activating platform admin identity: %w", err)
	}

	roleAssignments := buildRoleAssignments(masterTenant, identity.ID(), platformAdminRoles)

	// Persist identity and all role assignments in a single transaction.
	if err := repo.SaveIdentityWithRoles(masterCtx, identity, roleAssignments); err != nil {
		return fmt.Errorf("bootstrapping platform admin: %w", err)
	}

	slog.InfoContext(masterCtx, "platform admin bootstrapped successfully",
		"email", email,
		"roles", len(platformAdminRoles))
	return nil
}

// reconcileRoles ensures the existing platform admin has all required roles,
// creating any that are missing in a single atomic transaction.
func reconcileRoles(ctx context.Context, repo domain.Repository, identity *domain.Identity) error {
	existing, err := repo.FindRoleAssignments(ctx, identity.ID())
	if err != nil {
		return fmt.Errorf("fetching existing role assignments: %w", err)
	}

	// Build a set of active roles already assigned.
	assigned := make(map[string]bool, len(existing))
	for _, ra := range existing {
		if ra.IsActive() {
			assigned[string(ra.Role())] = true
		}
	}

	// Determine which roles are missing.
	var missing []auth.Role
	for _, role := range platformAdminRoles {
		if !assigned[role.String()] {
			missing = append(missing, role)
		}
	}

	if len(missing) == 0 {
		slog.InfoContext(ctx, "platform admin already exists with all required roles, skipping bootstrap",
			"email", identity.Email())
		return nil
	}

	slog.InfoContext(ctx, "platform admin exists but is missing roles, reconciling",
		"email", identity.Email(),
		"missing_roles", len(missing))

	roleAssignments := buildRoleAssignments(identity.TenantID(), identity.ID(), missing)

	// Persist all missing role assignments in a single transaction.
	if err := repo.SaveRoleAssignments(ctx, roleAssignments); err != nil {
		return fmt.Errorf("reconciling platform admin roles: %w", err)
	}
	return nil
}

// DemoUser holds the configuration for a demo user to be seeded on boot.
type DemoUser struct {
	Email    string
	Password string
	TenantID string
	Role     string // domain role string, e.g. "OPERATOR"
}

// loadDemoUsers reads demo user configuration from environment variables.
// Returns an empty slice if required variables are not set.
//
// DEMO_OPERATOR_TENANT supports comma-separated tenant IDs to create
// the same operator identity across multiple tenants (e.g., "volterra_energy,payg_energy").
// Each tenant gets its own namespaced identity with the same email and password.
func loadDemoUsers() []DemoUser {
	email := os.Getenv("DEMO_OPERATOR_EMAIL")
	password := os.Getenv("DEMO_OPERATOR_PASSWORD")
	if email == "" || password == "" {
		return nil
	}

	tenantID := os.Getenv("DEMO_OPERATOR_TENANT")
	if tenantID == "" {
		tenantID = "volterra"
	}

	tenants := strings.Split(tenantID, ",")
	users := make([]DemoUser, 0, len(tenants))
	for _, tid := range tenants {
		tid = strings.TrimSpace(tid)
		if tid == "" {
			continue
		}
		users = append(users, DemoUser{
			Email:    email,
			Password: password,
			TenantID: tid,
			Role:     string(domain.RoleOperator),
		})
	}
	return users
}

// SeedDemoUsers creates demo user identities from environment variables.
// Each user is created idempotently in its configured tenant with the specified role.
//
// Environment variables:
//   - DEMO_OPERATOR_EMAIL: Email address for the demo operator
//   - DEMO_OPERATOR_PASSWORD: Password for the demo operator
//   - DEMO_OPERATOR_TENANT: Tenant ID or comma-separated list (e.g., "volterra_energy,payg_energy")
func SeedDemoUsers(ctx context.Context, repo domain.Repository) error {
	if repo == nil {
		return ErrNilRepository
	}

	users := loadDemoUsers()
	if len(users) == 0 {
		slog.InfoContext(ctx, "demo user seeding skipped: no demo users configured")
		return nil
	}

	for _, u := range users {
		if err := seedDemoUser(ctx, repo, u); err != nil {
			// Skip tenants whose schemas have not been provisioned.
			// DEMO_OPERATOR_TENANT may list multiple tenants but not all
			// of them exist in every environment (e.g. payg_energy is
			// configured but only volterra_energy is provisioned on demo).
			if errors.Is(err, db.ErrTenantSchemaNotProvisioned) {
				slog.WarnContext(ctx, "demo user seeding skipped: tenant schema not provisioned",
					"email", u.Email,
					"tenant", u.TenantID)
				continue
			}
			return fmt.Errorf("seeding demo user %s: %w", u.Email, err)
		}
	}
	return nil
}

// seedDemoUser creates a single demo user identity idempotently.
func seedDemoUser(ctx context.Context, repo domain.Repository, u DemoUser) error {
	tid, err := tenant.NewTenantID(u.TenantID)
	if err != nil {
		return fmt.Errorf("invalid tenant ID %q: %w", u.TenantID, err)
	}
	tenantCtx := tenant.WithTenant(ctx, tid)

	// Check if the user already exists.
	existing, err := repo.FindByEmail(tenantCtx, u.Email)
	if err != nil && !errors.Is(err, domain.ErrIdentityNotFound) {
		return fmt.Errorf("checking for existing demo user: %w", err)
	}

	if existing != nil {
		// User already exists — reconcile the role.
		return reconcileDemoRole(tenantCtx, repo, existing, u.Role)
	}

	// Hash the password.
	hash, err := credentials.HashPassword(u.Password)
	if err != nil {
		return fmt.Errorf("hashing demo user password: %w", err)
	}

	// Build identity.
	identity, err := domain.NewIdentity(tid, u.Email)
	if err != nil {
		return fmt.Errorf("creating demo user identity: %w", err)
	}
	if err := identity.SetPassword(hash); err != nil {
		return fmt.Errorf("setting demo user password: %w", err)
	}
	if err := identity.Activate(); err != nil {
		return fmt.Errorf("activating demo user identity: %w", err)
	}

	now := time.Now()
	ra := domain.ReconstructRoleAssignment(
		uuid.New(),
		tid,
		identity.ID(),
		identity.ID(),
		domain.Role(u.Role),
		nil, nil, nil,
		now, now,
	)

	if err := repo.SaveIdentityWithRoles(tenantCtx, identity, []*domain.RoleAssignment{ra}); err != nil {
		return fmt.Errorf("persisting demo user: %w", err)
	}

	slog.InfoContext(tenantCtx, "demo user seeded successfully",
		"email", u.Email,
		"tenant", u.TenantID,
		"role", u.Role)
	return nil
}

// reconcileDemoRole ensures the demo user has the expected role assigned.
func reconcileDemoRole(ctx context.Context, repo domain.Repository, identity *domain.Identity, role string) error {
	existing, err := repo.FindRoleAssignments(ctx, identity.ID())
	if err != nil {
		return fmt.Errorf("fetching existing role assignments: %w", err)
	}

	for _, ra := range existing {
		if ra.IsActive() && string(ra.Role()) == role {
			slog.InfoContext(ctx, "demo user already exists with required role, skipping",
				"email", identity.Email(),
				"role", role)
			return nil
		}
	}

	slog.InfoContext(ctx, "demo user exists but missing role, adding",
		"email", identity.Email(),
		"role", role)

	now := time.Now()
	ra := domain.ReconstructRoleAssignment(
		uuid.New(),
		identity.TenantID(),
		identity.ID(),
		identity.ID(),
		domain.Role(role),
		nil, nil, nil,
		now, now,
	)

	return repo.SaveRoleAssignments(ctx, []*domain.RoleAssignment{ra})
}

// ProvisionAdminForTenant provisions the platform admin identity into a specific
// tenant's schema. This ensures the platform admin is visible when querying
// identities within that tenant (e.g., via ListIdentities).
//
// The function reads platform admin credentials from the same environment
// variables as Run (PLATFORM_ADMIN_EMAIL, PLATFORM_ADMIN_PASSWORD).
// If either is empty, the function is a no-op.
//
// Idempotency: If the admin already exists in the tenant schema, missing roles
// are reconciled atomically.
func ProvisionAdminForTenant(ctx context.Context, repo domain.Repository, tenantID tenant.TenantID) error {
	if repo == nil {
		return ErrNilRepository
	}

	email := os.Getenv("PLATFORM_ADMIN_EMAIL")
	password := os.Getenv("PLATFORM_ADMIN_PASSWORD")

	if email == "" || password == "" {
		slog.InfoContext(ctx, "tenant admin provisioning skipped: PLATFORM_ADMIN_EMAIL or PLATFORM_ADMIN_PASSWORD not set",
			"tenant_id", tenantID)
		return nil
	}

	tenantCtx := tenant.WithTenant(ctx, tenantID)

	// Check if admin already exists in this tenant.
	existing, err := repo.FindByEmail(tenantCtx, email)
	if err != nil && !errors.Is(err, domain.ErrIdentityNotFound) {
		return fmt.Errorf("checking for existing platform admin in tenant %s: %w", tenantID, err)
	}

	if existing != nil {
		return reconcileRoles(tenantCtx, repo, existing)
	}

	// Hash the password.
	hash, err := credentials.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing platform admin password: %w", err)
	}

	// Build domain objects.
	identity, err := domain.NewIdentity(tenantID, email)
	if err != nil {
		return fmt.Errorf("creating platform admin identity for tenant %s: %w", tenantID, err)
	}
	if err := identity.SetPassword(hash); err != nil {
		return fmt.Errorf("setting platform admin password: %w", err)
	}
	if err := identity.Activate(); err != nil {
		return fmt.Errorf("activating platform admin identity: %w", err)
	}

	roleAssignments := buildRoleAssignments(tenantID, identity.ID(), platformAdminRoles)

	if err := repo.SaveIdentityWithRoles(tenantCtx, identity, roleAssignments); err != nil {
		return fmt.Errorf("provisioning platform admin in tenant %s: %w", tenantID, err)
	}

	slog.InfoContext(tenantCtx, "platform admin provisioned in tenant",
		"tenant_id", tenantID,
		"email", email,
		"roles", len(platformAdminRoles))
	return nil
}

// AsPostProvisioningHook returns a post-provisioning hook that provisions the
// platform admin identity into newly created tenant schemas.
//
// Usage:
//
//	repo := persistence.NewRepository(db)
//	worker.RegisterPostProvisioningHook("admin-identity", bootstrap.AsPostProvisioningHook(repo))
func AsPostProvisioningHook(repo domain.Repository) func(ctx context.Context, tenantID tenant.TenantID) error {
	return func(ctx context.Context, tenantID tenant.TenantID) error {
		return ProvisionAdminForTenant(ctx, repo, tenantID)
	}
}

// buildRoleAssignments constructs RoleAssignment domain objects for each role.
// Bootstrap is a trusted system operation; ReconstructRoleAssignment is used to
// bypass the privilege hierarchy check that would otherwise require a granting identity.
func buildRoleAssignments(tid tenant.TenantID, identityID uuid.UUID, roles []auth.Role) []*domain.RoleAssignment {
	now := time.Now()
	assignments := make([]*domain.RoleAssignment, 0, len(roles))
	for _, role := range roles {
		ra := domain.ReconstructRoleAssignment(
			uuid.New(),
			tid,
			identityID,
			identityID, // self-granted by the system identity
			domain.Role(role.String()),
			nil,
			nil,
			nil,
			now,
			now,
		)
		assignments = append(assignments, ra)
	}
	return assignments
}
