// Package saga provides the SagaSeeder for seeding platform default saga definitions.
package saga

import (
	"context"
	"embed"
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
// Platform defaults are seeded with is_system=true and status=ACTIVE,
// making them read-only and immediately usable.
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
// Idempotency: Uses ON CONFLICT (name, version) DO NOTHING, so calling this
// multiple times for the same tenant is safe - existing sagas are skipped.
//
// The function:
//  1. Sets search_path to the tenant's schema
//  2. For each platform default saga:
//     - Reads the embedded .star file
//     - Inserts with is_system=true, status=ACTIVE, version=1
//     - Skips if already exists (idempotent)
func (s *Seeder) SeedTenant(ctx context.Context, tenantID tenant.TenantID) error {
	logger := s.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("seeding platform default sagas")

	defaults := PlatformDefaults()
	seededCount := 0

	err := s.withTenantTransaction(ctx, tenantID, func(tx pgx.Tx) error {
		for _, meta := range defaults {
			seeded, err := s.seedSaga(ctx, tx, meta)
			if err != nil {
				return fmt.Errorf("seed %s: %w", meta.Name, err)
			}
			if seeded {
				seededCount++
				logger.Debug("seeded platform saga", "name", meta.Name)
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

// seedSaga inserts a single platform saga if it doesn't already exist.
// Returns true if the saga was inserted, false if it already existed.
func (s *Seeder) seedSaga(ctx context.Context, tx pgx.Tx, meta Metadata) (bool, error) {
	// Read embedded script
	script, err := s.readEmbeddedScript(meta.Filename)
	if err != nil {
		return false, err
	}

	// Generate deterministic UUID based on name for idempotency
	// Using UUIDv5 with the saga name ensures the same saga always gets the same ID
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian."+meta.Name))

	now := time.Now()

	// Insert with ON CONFLICT DO NOTHING for idempotency
	// Note: We insert directly as ACTIVE since these are platform defaults
	query := `
		INSERT INTO saga_definition (
			id, name, version, script, status, is_system,
			display_name, description, created_at, updated_at, activated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
		ON CONFLICT (name, version) DO NOTHING`

	result, err := tx.Exec(ctx, query,
		id,        // id
		meta.Name, // name
		1,         // version (always 1 for platform defaults)
		script,    // script
		"ACTIVE",  // status (platform defaults are immediately active)
		true,      // is_system (platform defaults are system sagas)
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

// readEmbeddedScript reads a saga script from the embedded filesystem.
func (s *Seeder) readEmbeddedScript(filename string) (string, error) {
	content, err := defaultSagas.ReadFile(path.Join("defaults", filename))
	if err != nil {
		return "", fmt.Errorf("read embedded file %s: %w", filename, err)
	}
	return strings.TrimSpace(string(content)), nil
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
