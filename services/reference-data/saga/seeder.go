// Package saga provides the SagaSeeder for seeding platform default saga definitions.
package saga

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

//go:embed all:defaults
var defaultSagas embed.FS

// ErrEmbeddedScriptMissing is returned when a saga's embedded script file is not found.
var ErrEmbeddedScriptMissing = errors.New("embedded script not found for saga")

// versionFilenameRegex matches version filenames like "v1.0.0.star".
var versionFilenameRegex = regexp.MustCompile(`^v(\d+\.\d+\.\d+)\.star$`)

// humanizeName converts a saga name like "current_account_withdrawal" to "Current Account Withdrawal".
func humanizeName(name string) string {
	words := strings.Split(name, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// Metadata defines metadata for a platform default saga.
type Metadata struct {
	// Name is the saga identifier (e.g., "current_account_withdrawal").
	Name string

	// DisplayName is the human-readable name.
	DisplayName string

	// Description provides context about the saga.
	Description string

	// Filename is the directory name within defaults/ (e.g., "withdrawal").
	Filename string
}

// PlatformDefaults discovers sagas from the embedded directory structure.
// Each subdirectory under defaults/ represents a saga.
func PlatformDefaults() ([]Metadata, error) {
	entries, err := defaultSagas.ReadDir("defaults")
	if err != nil {
		return nil, fmt.Errorf("read embedded defaults directory: %w", err)
	}

	defaults := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sagaDir := entry.Name()

		// Convention: directory name maps to saga name
		// deposit/ -> current_account_deposit
		// withdrawal/ -> current_account_withdrawal
		// payment_execution/ -> payment_execution (no prefix for non-account sagas)
		fullName := sagaNameFromDir(sagaDir)

		defaults = append(defaults, Metadata{
			Name:        fullName,
			DisplayName: humanizeName(fullName),
			Description: fmt.Sprintf("Platform default saga for %s operations.", strings.ReplaceAll(sagaDir, "_", " ")),
			Filename:    sagaDir,
		})
	}

	return defaults, nil
}

// sagaNameFromDir converts a directory name to the full saga name.
// For current account sagas, the convention is to prefix with "current_account_".
// Other sagas (payment_execution, dunning_escalation, etc.) use the directory name as-is.
func sagaNameFromDir(dirName string) string {
	switch dirName {
	case "deposit":
		return "current_account_deposit"
	case "withdrawal":
		return "current_account_withdrawal"
	default:
		return dirName
	}
}

// Seeder seeds platform default saga definitions into tenant schemas.
// Platform defaults are seeded with is_system=true, status=ACTIVE, and the
// actual script content copied directly into the tenant's saga_definition table.
//
// The seeder copies script content from embedded .star files:
//   - Tenant saga_definition rows have script=<content> (self-contained)
//   - No cross-schema references to public.platform_saga_definition
//   - Complete tenant isolation - each tenant has its own copy of all saga scripts
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
// Idempotency: Uses ON CONFLICT (name, version) DO NOTHING, so calling this
// multiple times for the same tenant is safe - existing sagas are skipped.
//
// The function:
//  1. Loads saga scripts from embedded .star files
//  2. Sets search_path to the tenant's schema
//  3. For each platform default saga:
//     - Creates a saga_definition with the actual script content
//     - Inserts with is_system=true, status=ACTIVE, version=1
//     - Skips if already exists (idempotent)
func (s *Seeder) SeedTenant(ctx context.Context, tenantID tenant.TenantID) error {
	logger := s.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("seeding platform default sagas")

	defaults, err := PlatformDefaults()
	if err != nil {
		logger.Error("failed to read embedded defaults", "error", err)
		return err
	}

	// Load scripts from embedded filesystem
	scripts, err := GetEmbeddedScripts()
	if err != nil {
		logger.Error("failed to load embedded scripts", "error", err)
		return err
	}

	seededCount := 0

	err = s.withTenantTransaction(ctx, tenantID, func(tx pgx.Tx) error {
		for _, meta := range defaults {
			// Look up the script content from embedded files using the flat key
			scriptKey := meta.Filename + ".star"
			script, ok := scripts[scriptKey]
			if !ok {
				return fmt.Errorf("%w: %s (key: %s)", ErrEmbeddedScriptMissing, meta.Name, scriptKey)
			}

			seeded, seedErr := s.seedSaga(ctx, tx, meta, script)
			if seedErr != nil {
				return fmt.Errorf("seed %s: %w", meta.Name, seedErr)
			}
			if seeded {
				seededCount++
				logger.Debug("seeded platform saga with script",
					"name", meta.Name)
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

	// Set search_path to tenant schema only (no public fallback)
	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
	if _, err := tx.Exec(ctx, query); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// seedSaga inserts a single platform saga with its script content.
// The saga_definition row has the actual script content (self-contained).
// Returns true if the saga was inserted, false if it already existed.
func (s *Seeder) seedSaga(ctx context.Context, tx pgx.Tx, meta Metadata, script string) (bool, error) {
	// Generate deterministic UUID based on name for idempotency
	// Using UUIDv5 with the saga name ensures the same saga always gets the same ID
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("saga.meridian."+meta.Name))

	now := time.Now()

	// Insert with ON CONFLICT DO NOTHING for idempotency
	// Script content is copied directly - no cross-schema reference needed
	query := `
		INSERT INTO saga_definition (
			id, name, version, script, status, is_system,
			display_name, description,
			created_at, updated_at, activated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8,
			$9, $10, $11
		)
		ON CONFLICT (name, version) DO NOTHING`

	result, err := tx.Exec(ctx, query,
		id,        // id
		meta.Name, // name
		1,         // version (always 1 for platform defaults)
		script,    // script (actual content, not a reference)
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

// GetEmbeddedScripts returns all embedded saga scripts keyed by saga directory name.
// The returned map uses keys like "deposit/v1.0.0.star" for the new directory structure.
// For backward compatibility in tests, it also provides flat keys like "deposit.star"
// pointing to the highest semver version file found for each saga.
func GetEmbeddedScripts() (map[string]string, error) {
	scripts := make(map[string]string)

	// Read top-level directories
	entries, err := defaultSagas.ReadDir("defaults")
	if err != nil {
		return nil, fmt.Errorf("read defaults directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if err := loadSagaDirScripts(scripts, entry.Name()); err != nil {
			return nil, err
		}
	}

	return scripts, nil
}

// loadSagaDirScripts reads all version files from a saga directory into the scripts map.
// It stores each file with a full path key (e.g. "deposit/v1.0.0.star") and adds a
// backward-compatible flat key (e.g. "deposit.star") pointing to the highest semver content.
func loadSagaDirScripts(scripts map[string]string, sagaDir string) error {
	dirPath := path.Join("defaults", sagaDir)

	versionEntries, err := defaultSagas.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("read saga directory %s: %w", sagaDir, err)
	}

	var latestContent string
	var latestVersion string
	for _, vEntry := range versionEntries {
		if vEntry.IsDir() || !strings.HasSuffix(vEntry.Name(), ".star") {
			continue
		}

		filePath := path.Join(dirPath, vEntry.Name())
		content, err := defaultSagas.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read %s: %w", filePath, err)
		}

		trimmed := strings.TrimSpace(string(content))
		scripts[sagaDir+"/"+vEntry.Name()] = trimmed

		// Track highest semver for backward-compatible flat key
		matches := versionFilenameRegex.FindStringSubmatch(vEntry.Name())
		if len(matches) > 1 && isSemverGreater(matches[1], latestVersion) {
			latestVersion = matches[1]
			latestContent = trimmed
		}
	}

	if latestContent != "" {
		scripts[sagaDir+".star"] = latestContent
	}

	return nil
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

// isSemverGreater returns true if version a is greater than version b.
// Both must be in "major.minor.patch" format. An empty b always returns true.
func isSemverGreater(a, b string) bool {
	if b == "" {
		return true
	}
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < 3 && i < len(ap) && i < len(bp); i++ {
		ai, _ := strconv.Atoi(ap[i])
		bi, _ := strconv.Atoi(bp[i])
		if ai != bi {
			return ai > bi
		}
	}
	return false
}
