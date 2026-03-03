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
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/platform/auth"
	platformdb "github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

// MasterTenantID is the well-known tenant ID for the master/platform tenant.
const MasterTenantID = "meridian_master"

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
//   - If an admin already exists, any missing roles are reconciled.
//   - Identity creation and role assignments are committed atomically.
func Run(ctx context.Context, db *gorm.DB) error {
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

	repo := persistence.NewRepository(db)

	// Check if an admin already exists (outside the transaction — idempotency guard).
	existing, err := repo.FindByEmail(masterCtx, email)
	if err != nil && !errors.Is(err, domain.ErrIdentityNotFound) {
		return fmt.Errorf("checking for existing platform admin: %w", err)
	}

	if existing != nil {
		// Admin already exists — reconcile any missing roles atomically.
		return reconcileRoles(masterCtx, db, existing)
	}

	// Hash the password before opening the transaction.
	hash, err := credentials.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing platform admin password: %w", err)
	}

	// Build the domain objects before the transaction.
	identity, err := domain.NewIdentity(email)
	if err != nil {
		return fmt.Errorf("creating platform admin identity: %w", err)
	}
	if err := identity.SetPassword(hash); err != nil {
		return fmt.Errorf("setting platform admin password: %w", err)
	}
	if err := identity.Activate(); err != nil {
		return fmt.Errorf("activating platform admin identity: %w", err)
	}

	roleAssignments := buildRoleAssignments(identity.ID(), platformAdminRoles)

	// Persist identity and all role assignments atomically.
	if err := platformdb.WithGormTenantTransaction(masterCtx, db, func(tx *gorm.DB) error {
		txRepo := persistence.NewRepository(tx)
		if err := txRepo.Save(masterCtx, identity); err != nil {
			return fmt.Errorf("save identity: %w", err)
		}
		for _, ra := range roleAssignments {
			if err := txRepo.SaveRoleAssignment(masterCtx, ra); err != nil {
				return fmt.Errorf("save role assignment %s: %w", ra.Role(), err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("bootstrapping platform admin: %w", err)
	}

	slog.InfoContext(masterCtx, "platform admin bootstrapped successfully",
		"email", email,
		"roles", len(platformAdminRoles))
	return nil
}

// reconcileRoles ensures the existing platform admin has all required roles,
// creating any that are missing. All changes are committed atomically.
func reconcileRoles(ctx context.Context, db *gorm.DB, identity *domain.Identity) error {
	repo := persistence.NewRepository(db)

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

	roleAssignments := buildRoleAssignments(identity.ID(), missing)

	return platformdb.WithGormTenantTransaction(ctx, db, func(tx *gorm.DB) error {
		txRepo := persistence.NewRepository(tx)
		for _, ra := range roleAssignments {
			if err := txRepo.SaveRoleAssignment(ctx, ra); err != nil {
				return fmt.Errorf("save missing role assignment %s: %w", ra.Role(), err)
			}
		}
		return nil
	})
}

// buildRoleAssignments constructs RoleAssignment domain objects for each role.
// Bootstrap is a trusted system operation; ReconstructRoleAssignment is used to
// bypass the privilege hierarchy check that would otherwise require a granting identity.
func buildRoleAssignments(identityID uuid.UUID, roles []auth.Role) []*domain.RoleAssignment {
	now := time.Now()
	assignments := make([]*domain.RoleAssignment, 0, len(roles))
	for _, role := range roles {
		ra := domain.ReconstructRoleAssignment(
			uuid.New(),
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
