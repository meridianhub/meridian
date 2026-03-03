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
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
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
// If an admin identity already exists in meridian_master, the function skips
// creation and returns nil (idempotent).
func Run(ctx context.Context, repo domain.Repository) error {
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
		slog.InfoContext(masterCtx, "platform admin already exists, skipping bootstrap",
			"email", email)
		return nil
	}

	// Hash the password before creating the identity.
	hash, err := credentials.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing platform admin password: %w", err)
	}

	// Create the identity.
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

	if err := repo.Save(masterCtx, identity); err != nil {
		return fmt.Errorf("saving platform admin identity: %w", err)
	}

	// Assign platform admin roles. Bootstrap is a trusted system operation,
	// so we use ReconstructRoleAssignment to bypass the privilege hierarchy check.
	systemID := identity.ID()
	now := time.Now()
	for _, role := range platformAdminRoles {
		ra := domain.ReconstructRoleAssignment(
			uuid.New(),
			identity.ID(),
			systemID,
			domain.Role(role.String()),
			nil,
			nil,
			nil,
			now,
			now,
		)
		if err := repo.SaveRoleAssignment(masterCtx, ra); err != nil {
			return fmt.Errorf("saving role assignment %s: %w", role, err)
		}
	}

	slog.InfoContext(masterCtx, "platform admin bootstrapped successfully",
		"email", email,
		"roles", len(platformAdminRoles))
	return nil
}
