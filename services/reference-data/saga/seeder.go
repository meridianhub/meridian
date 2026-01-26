// Package saga provides the SagaSeeder for seeding platform default saga definitions.
package saga

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

//go:embed defaults/*.star
var defaultSagas embed.FS

// ErrPlatformSagaNotSynced is returned when a platform saga referenced by the seeder
// has not been synced to the platform_saga_definition table.
var ErrPlatformSagaNotSynced = errors.New("platform saga not synced: run PlatformSync.SyncPlatformDefaults first")

// Metadata defines metadata for a platform default saga.
type Metadata struct {
	// Name is the saga identifier (e.g., "current_account_withdrawal").
	Name string

	// DisplayName is the human-readable name.
	DisplayName string

	// Description provides context about the saga.
	Description string

	// Filename is the embedded file name (e.g., "withdrawal.star").
	Filename string
}

// PlatformDefaults returns the metadata for all platform default sagas.
// The order determines seeding order.
func PlatformDefaults() []Metadata {
	return []Metadata{
		{
			Name:        "current_account_withdrawal",
			DisplayName: "Current Account Withdrawal",
			Description: "Platform default saga for processing withdrawals from current accounts. " +
				"Coordinates position logging, booking log creation, double-entry postings, and account persistence.",
			Filename: "withdrawal.star",
		},
		{
			Name:        "current_account_deposit",
			DisplayName: "Current Account Deposit",
			Description: "Platform default saga for processing deposits to current accounts. " +
				"Coordinates position logging, booking log creation, double-entry postings, and account persistence.",
			Filename: "deposit.star",
		},
		{
			Name:        "payment_execution",
			DisplayName: "Payment Order Execution",
			Description: "Platform default saga for executing payment orders. " +
				"Coordinates lien creation, gateway submission, ledger posting, and lien execution.",
			Filename: "payment_execution.star",
		},
	}
}

// Seeder seeds platform default saga definitions into tenant schemas.
// Platform defaults are seeded with is_system=true, status=ACTIVE, and a platform_ref
// pointing to the corresponding entry in public.platform_saga_definition.
//
// The seeder creates reference-based entries (platform_ref) rather than copying
// script content. This means:
//   - Tenant saga_definition rows have script=NULL and platform_ref set
//   - The actual script is resolved at query time via COALESCE with platform_saga_definition
//   - Platform script updates automatically propagate to all tenants using references
//   - Tenants can override by creating their own saga with a custom script
type Seeder struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewSeeder creates a new saga seeder.
func NewSeeder(pool *pgxpool.Pool) *Seeder {
	return &Seeder{
		pool:   pool,
		logger: slog.Default().With("component", "saga_seeder"),
	}
}

// SeedTenant seeds all platform default sagas into a specific tenant's schema.
// This is called during tenant provisioning after the schema is created.
//
// Prerequisites: PlatformSync.SyncPlatformDefaults must have been called first
// to populate the public.platform_saga_definition table.
//
// Idempotency: Uses ON CONFLICT (name, version) DO NOTHING, so calling this
// multiple times for the same tenant is safe - existing sagas are skipped.
//
// The function:
//  1. Looks up each platform default in public.platform_saga_definition
//  2. Sets search_path to the tenant's schema
//  3. For each platform default saga:
//     - Creates a saga_definition with platform_ref (no script copy)
//     - Inserts with is_system=true, status=ACTIVE, version=1
//     - Skips if already exists (idempotent)
func (s *Seeder) SeedTenant(ctx context.Context, tenantID tenant.TenantID) error {
	logger := s.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("seeding platform default sagas")

	defaults := PlatformDefaults()

	// Look up platform saga IDs from public.platform_saga_definition
	platformRefs, err := s.lookupPlatformRefs(ctx, defaults)
	if err != nil {
		logger.Error("failed to look up platform saga references", "error", err)
		return err
	}

	seededCount := 0

	err = s.withTenantTransaction(ctx, tenantID, func(tx pgx.Tx) error {
		for _, meta := range defaults {
			platformRefID := platformRefs[meta.Name]
			seeded, seedErr := s.seedSaga(ctx, tx, meta, platformRefID)
			if seedErr != nil {
				return fmt.Errorf("seed %s: %w", meta.Name, seedErr)
			}
			if seeded {
				seededCount++
				logger.Debug("seeded platform saga reference",
					"name", meta.Name,
					"platform_ref", platformRefID)
			} else {
				logger.Debug("platform saga already exists", "name", meta.Name)
			}
		}
		return nil
	})
	if err != nil {
		logger.Error("failed to seed platform sagas", "error", err)
		return err
	}

	logger.Info("platform sagas seeded",
		"total", len(defaults),
		"seeded", seededCount,
		"skipped", len(defaults)-seededCount)
	return nil
}

// lookupPlatformRefs resolves the platform_saga_definition IDs for each default saga.
// Returns a map of saga name to platform saga definition UUID.
func (s *Seeder) lookupPlatformRefs(ctx context.Context, defaults []Metadata) (map[string]uuid.UUID, error) {
	refs := make(map[string]uuid.UUID, len(defaults))

	for _, meta := range defaults {
		var platformID uuid.UUID
		err := s.pool.QueryRow(ctx,
			`SELECT id FROM public.platform_saga_definition WHERE name = $1`,
			meta.Name,
		).Scan(&platformID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", ErrPlatformSagaNotSynced, meta.Name)
			}
			return nil, fmt.Errorf("lookup platform saga %s: %w", meta.Name, err)
		}
		refs[meta.Name] = platformID
	}

	return refs, nil
}

// withTenantTransaction executes a function within a transaction scoped to a tenant's schema.
func (s *Seeder) withTenantTransaction(ctx context.Context, tenantID tenant.TenantID, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Set search_path to tenant schema
	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	if _, err := tx.Exec(ctx, query); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// seedSaga inserts a single platform saga reference if it doesn't already exist.
// The saga_definition row has script=NULL and platform_ref pointing to the
// platform_saga_definition entry. The script is resolved at query time.
// Returns true if the saga was inserted, false if it already existed.
func (s *Seeder) seedSaga(ctx context.Context, tx pgx.Tx, meta Metadata, platformRefID uuid.UUID) (bool, error) {
	// Generate deterministic UUID based on name for idempotency
	// Using UUIDv5 with the saga name ensures the same saga always gets the same ID
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian."+meta.Name))

	now := time.Now()

	// Insert with ON CONFLICT DO NOTHING for idempotency
	// Note: script is NULL because the saga inherits its script via platform_ref
	query := `
		INSERT INTO saga_definition (
			id, name, version, script, status, is_system,
			platform_ref, display_name, description,
			created_at, updated_at, activated_at
		) VALUES (
			$1, $2, $3, NULL, $4, $5,
			$6, $7, $8,
			$9, $10, $11
		)
		ON CONFLICT (name, version) DO NOTHING`

	result, err := tx.Exec(ctx, query,
		id,            // id
		meta.Name,     // name
		1,             // version (always 1 for platform defaults)
		"ACTIVE",      // status (platform defaults are immediately active)
		true,          // is_system (platform defaults are system sagas)
		platformRefID, // platform_ref -> public.platform_saga_definition
		meta.DisplayName,
		meta.Description,
		now, // created_at
		now, // updated_at
		now, // activated_at (already active)
	)
	if err != nil {
		return false, fmt.Errorf("insert saga: %w", err)
	}

	// Check if row was inserted
	return result.RowsAffected() > 0, nil
}

// GetEmbeddedScripts returns all embedded saga scripts.
// Useful for validation and testing.
func GetEmbeddedScripts() (map[string]string, error) {
	scripts := make(map[string]string)

	entries, err := defaultSagas.ReadDir("defaults")
	if err != nil {
		return nil, fmt.Errorf("read defaults directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".star") {
			continue
		}

		content, err := defaultSagas.ReadFile(path.Join("defaults", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}

		scripts[entry.Name()] = strings.TrimSpace(string(content))
	}

	return scripts, nil
}

// AsPostProvisioningHook returns a function compatible with provisioner.PostProvisioningHook
// for seeding platform default sagas into newly provisioned tenants.
//
// Usage:
//
//	seeder := saga.NewSeeder(referenceDataPool)
//	config.PostProvisioningHooks = append(config.PostProvisioningHooks, seeder.AsPostProvisioningHook())
func (s *Seeder) AsPostProvisioningHook() func(ctx context.Context, tenantID tenant.TenantID) error {
	return s.SeedTenant
}
