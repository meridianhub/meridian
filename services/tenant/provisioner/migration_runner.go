package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internalmigrations "github.com/meridianhub/meridian/internal/migrations"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

// migration represents a single migration file.
type migration struct {
	Filename string
	Version  string
	Content  string
}

// ReconcileMigrations detects and applies new migrations to existing tenant schemas.
//
// This method addresses schema drift that occurs when services add new migrations
// after tenants are created. It scans migration directories, compares with
// the recorded MigrationVersion for each service, and applies any newer migrations.
//
// If tenantID is nil, all active tenants are reconciled. Individual tenant failures
// don't stop processing of other tenants - errors are collected and returned.
func (p *PostgresProvisioner) ReconcileMigrations(ctx context.Context, tenantID *tenant.TenantID) (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	logger := p.logger.With("operation", "reconcile_migrations")

	// Determine which tenants to reconcile
	tenantsToReconcile, err := p.getTenantsToReconcile(ctx, tenantID)
	if err != nil {
		logger.Error("failed to get tenants to reconcile", "error", err)
		return 0, []string{fmt.Sprintf("get tenants: %v", err)}
	}

	if len(tenantsToReconcile) == 0 {
		logger.Info("no tenants to reconcile")
		return 0, nil
	}

	logger.Info("starting migration reconciliation", "tenant_count", len(tenantsToReconcile))

	var (
		reconciledCount int
		errs            []string
	)

	for _, tid := range tenantsToReconcile {
		if ctx.Err() != nil {
			errs = append(errs, fmt.Sprintf("context cancelled: %v", ctx.Err()))
			break
		}

		applied, err := p.reconcileTenantMigrations(ctx, tid)
		if err != nil {
			logger.Error("failed to reconcile tenant",
				"tenant_id", tid.String(),
				"error", err)
			errs = append(errs, fmt.Sprintf("%s: %v", tid.String(), err))
			continue
		}

		if applied {
			reconciledCount++
			logger.Info("tenant migrations reconciled", "tenant_id", tid.String())
		}
	}

	logger.Info("migration reconciliation completed",
		"reconciled_count", reconciledCount,
		"error_count", len(errs))

	return reconciledCount, errs
}

// getTenantsToReconcile returns the list of tenant IDs to reconcile.
// If tenantID is non-nil, returns just that tenant. Otherwise returns all active tenants.
func (p *PostgresProvisioner) getTenantsToReconcile(ctx context.Context, tenantID *tenant.TenantID) ([]tenant.TenantID, error) {
	if tenantID != nil {
		return []tenant.TenantID{*tenantID}, nil
	}

	// Query all active tenants from the platform database
	var entities []provisioningEntity
	result := p.platformDB.WithContext(bypassCtx(ctx)).
		Where("state = ?", string(StateActive)).
		Find(&entities)
	if result.Error != nil {
		return nil, fmt.Errorf("query active tenants: %w", result.Error)
	}

	tenants := make([]tenant.TenantID, 0, len(entities))
	for _, entity := range entities {
		tid, err := tenant.NewTenantID(entity.TenantID)
		if err != nil {
			p.logger.Warn("invalid tenant ID in provisioning table",
				"tenant_id", entity.TenantID,
				"error", err)
			continue
		}
		tenants = append(tenants, tid)
	}

	return tenants, nil
}

// reconcileTenantMigrations applies new migrations to a single tenant.
// Returns true if any migrations were applied, false otherwise.
func (p *PostgresProvisioner) reconcileTenantMigrations(ctx context.Context, tenantID tenant.TenantID) (bool, error) {
	logger := p.logger.With("tenant_id", tenantID.String())

	// Get current provisioning status
	status, err := p.getProvisioningStatusLocked(ctx, tenantID)
	if err != nil {
		return false, fmt.Errorf("get provisioning status: %w", err)
	}

	// Only reconcile active tenants
	if status.State != StateActive {
		logger.Debug("skipping non-active tenant", "state", status.State)
		return false, nil
	}

	schemaName := tenantID.SchemaName()
	anyMigrationsApplied := false

	// Check each service for new migrations
	for _, svc := range p.config.Services {
		applied, err := p.reconcileServiceMigrations(ctx, status, svc, schemaName, logger)
		if err != nil {
			return anyMigrationsApplied, err
		}
		if applied {
			anyMigrationsApplied = true
		}
	}

	// Save updated status if any migrations were applied
	if anyMigrationsApplied {
		status.UpdatedAt = timeNow()
		if err := p.saveProvisioningStatus(ctx, status); err != nil {
			return true, fmt.Errorf("save updated status: %w", err)
		}
	}

	return anyMigrationsApplied, nil
}

// reconcileServiceMigrations applies new migrations for a single service in a tenant schema.
// Returns true if any migrations were applied.
func (p *PostgresProvisioner) reconcileServiceMigrations(ctx context.Context, status *ProvisioningStatus, svc ServiceConfig, schemaName string, logger *slog.Logger) (bool, error) {
	svcStatus := status.getServiceStatus(svc.Name)
	currentVersion := ""
	if svcStatus != nil {
		currentVersion = svcStatus.MigrationVersion
	}

	if currentVersion == "" {
		logger.Warn("service has no recorded migration version, skipping reconciliation",
			"service", svc.Name,
			"hint", "this may indicate the service was added after initial provisioning")
		return false, nil
	}

	migrations, err := p.readMigrationFiles(svc.MigrationPath)
	if err != nil {
		return false, fmt.Errorf("read migrations for %s: %w", svc.Name, err)
	}

	newMigrations := filterMigrationsAfter(migrations, currentVersion)
	if len(newMigrations) == 0 {
		logger.Debug("no new migrations for service", "service", svc.Name)
		return false, nil
	}

	logger.Info("applying new migrations",
		"service", svc.Name,
		"current_version", currentVersion,
		"new_migration_count", len(newMigrations))

	serviceDB, ok := p.serviceDbs[svc.Name]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrServiceDatabaseNotFound, svc.Name)
	}

	latestVersion, err := p.applyMigrationList(ctx, serviceDB, schemaName, newMigrations)
	if err != nil {
		return false, fmt.Errorf("apply migrations for %s: %w", svc.Name, err)
	}

	if svcStatus != nil {
		svcStatus.MigrationVersion = latestVersion
	}

	logger.Debug("service migrations applied",
		"service", svc.Name,
		"new_version", latestVersion)

	return true, nil
}

// filterMigrationsAfter returns migrations with version > currentVersion.
// Migrations are already sorted by filename/version from readMigrationFiles.
//
// If currentVersion is empty, returns nil as a safety guard. The caller
// (reconcileTenantMigrations) handles empty versions explicitly with a warning.
func filterMigrationsAfter(migrations []migration, currentVersion string) []migration {
	if currentVersion == "" {
		return nil
	}

	var result []migration
	for _, mig := range migrations {
		if mig.Version > currentVersion {
			result = append(result, mig)
		}
	}
	return result
}

// applyServiceMigrationsToDB applies all migration files for a service to the tenant schema
// in the specified service database.
//
// IDEMPOTENCY: If a migration creates objects that already exist, the error is caught by
// isAlreadyExistsError and the migration is marked as applied. This allows retries after
// partial failures where some tables were created but the version wasn't recorded.
//
// Returns the version string of the last applied migration.
func (p *PostgresProvisioner) applyServiceMigrationsToDB(ctx context.Context, db *gorm.DB, schemaName string, svc ServiceConfig) (string, error) {
	// Read migration files
	migrations, err := p.readMigrationFiles(svc.MigrationPath)
	if err != nil {
		return "", fmt.Errorf("read migrations: %w", err)
	}

	if len(migrations) == 0 {
		return "", nil
	}

	return p.applyMigrationList(ctx, db, schemaName, migrations)
}

// applyMigrationList applies a specific list of migrations to a tenant schema.
// This is extracted from applyServiceMigrationsToDB to support both initial
// provisioning (all migrations) and reconciliation (subset of migrations).
//
// IDEMPOTENCY: Same guarantees as applyServiceMigrationsToDB - objects that already
// exist are silently skipped via isAlreadyExistsError.
func (p *PostgresProvisioner) applyMigrationList(ctx context.Context, db *gorm.DB, schemaName string, migrations []migration) (string, error) {
	if len(migrations) == 0 {
		return "", nil
	}

	// Set search_path to the tenant schema for unqualified table names
	setPathQuery := fmt.Sprintf("SET search_path TO %s", quoteIdentifier(schemaName))

	var lastVersion string
	for _, mig := range migrations {
		if ctx.Err() != nil {
			return lastVersion, ctx.Err()
		}

		err := p.applyMigrationInTransaction(ctx, db, schemaName, setPathQuery, mig)
		if err != nil {
			// IDEMPOTENCY: If error indicates objects already exist (duplicate_table,
			// duplicate_schema, duplicate_object), treat as success. This handles the
			// case where a previous provisioning attempt partially completed.
			if isAlreadyExistsError(err) {
				lastVersion = mig.Version
				continue
			}
			return lastVersion, err
		}

		lastVersion = mig.Version
	}

	return lastVersion, nil
}

// applyMigrationInTransaction runs a single migration file inside its own
// transaction: SET search_path, process+adapt the SQL, then execute each
// statement. Extracted from applyMigrationList to keep cognitive complexity
// under the architecture baseline.
func (p *PostgresProvisioner) applyMigrationInTransaction(ctx context.Context, db *gorm.DB, schemaName, setPathQuery string, mig migration) error {
	return db.WithContext(bypassCtx(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(setPathQuery).Error; err != nil {
			return fmt.Errorf("set search_path: %w", err)
		}

		processedSQL := p.processMigrationSQL(mig.Content, schemaName)
		statements := splitSQLStatements(processedSQL)

		// When running against PostgreSQL, rewrite CockroachDB-specific DDL
		// (e.g., DROP INDEX CASCADE for unique constraints) to Postgres-compatible
		// equivalents on a per-statement basis AFTER splitSQLStatements. The
		// adapter may wrap statements in DO $compat$ BEGIN ...; ...; END $compat$
		// blocks whose internal semicolons would otherwise be split as separate
		// statements — splitSQLStatements does not recognize dollar-quoted bodies.
		// Splitting first and then adapting keeps each adapted unit intact.
		usePostgresAdapter := internalmigrations.DriverFromEnv() == internalmigrations.DriverPostgres

		for _, stmt := range statements {
			if usePostgresAdapter {
				// AdaptCockroachDDLForPostgres's regex matches patterns
				// terminated by `;`; splitSQLStatements strips terminators,
				// so re-add one before adaptation.
				stmt = internalmigrations.AdaptCockroachDDLForPostgres(stmt + ";")
			}
			if err := tx.Exec(stmt).Error; err != nil {
				return fmt.Errorf("execute migration %s: %w", mig.Filename, err)
			}
		}

		return nil
	})
}

// readMigrationFiles reads all .sql files from the migration path, sorted by filename.
func (p *PostgresProvisioner) readMigrationFiles(migrationPath string) ([]migration, error) {
	entries, err := os.ReadDir(migrationPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No migrations directory is valid
		}
		return nil, fmt.Errorf("read directory: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(migrationPath, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read file %s: %w", entry.Name(), err)
		}

		// Extract version from filename (e.g., "20251208211142_initial.sql" -> "20251208211142")
		version := strings.TrimSuffix(entry.Name(), ".sql")
		if idx := strings.Index(version, "_"); idx > 0 {
			version = version[:idx]
		}

		migrations = append(migrations, migration{
			Filename: entry.Name(),
			Version:  version,
			Content:  string(content),
		})
	}

	// Sort by filename to ensure consistent order
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Filename < migrations[j].Filename
	})

	return migrations, nil
}

// processMigrationSQL processes migration SQL to work with dynamic schema names.
// It handles both unqualified table names and hardcoded schema references.
//
// Security: The schemaName parameter is derived from TenantID.SchemaName(),
// which is validated at construction to contain only alphanumeric characters and
// underscores (regex: ^[a-zA-Z0-9_]{1,50}$). The "org_" prefix is added and the
// string is lowercased, making SQL injection impossible through this path.
// The string replacement is safe because the schema name cannot contain quotes,
// semicolons, or other SQL control characters.
func (p *PostgresProvisioner) processMigrationSQL(sql, schemaName string) string {
	// Remove CREATE SCHEMA statements - we already created the schema
	lines := strings.Split(sql, "\n")
	filteredLines := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.ToUpper(line))
		if strings.HasPrefix(trimmed, "CREATE SCHEMA") {
			continue
		}
		filteredLines = append(filteredLines, line)
	}
	sql = strings.Join(filteredLines, "\n")

	// Replace hardcoded schema references with the tenant schema.
	// Applied per-statement so COMMENT ON COLUMN can be skipped: in
	// COMMENT ON COLUMN "party"."attributes", the second identifier is a
	// column name, not a table name. The naive rewriter would replace
	// the table name "party" with the schema name, producing
	// COMMENT ON COLUMN "org_tenant"."attributes" which Postgres parses
	// as schema.table (missing the column component) and fails with
	// "relation does not exist".
	schemaPatterns := []string{
		"current_account",
		"party",
		"position_keeping",
		"financial_accounting",
		"payment_order",
	}

	statements := splitSQLStatements(sql)
	rewritten := make([]string, 0, len(statements))
	for _, stmt := range statements {
		if isCommentStatement(stmt) {
			rewritten = append(rewritten, stmt)
			continue
		}
		for _, pattern := range schemaPatterns {
			// Replace "schema"."table" with "tenant_schema"."table"
			stmt = strings.ReplaceAll(stmt, `"`+pattern+`".`, `"`+schemaName+`".`)
			// Also handle unquoted references
			stmt = strings.ReplaceAll(stmt, pattern+".", schemaName+".")
		}
		rewritten = append(rewritten, stmt)
	}

	// Rejoin with semicolon terminators so the downstream splitSQLStatements
	// call in applyMigrationList produces the same statement list.
	if len(rewritten) == 0 {
		return ""
	}
	return strings.Join(rewritten, ";\n") + ";"
}

// isCommentStatement reports whether the given SQL statement is a COMMENT
// statement (COMMENT ON TABLE/COLUMN/INDEX/etc). These must not be rewritten
// by the schema-pattern replacer because "table"."column" in COMMENT ON COLUMN
// is a column reference, not a schema qualifier.
//
// Leading whitespace, line comments (-- ...), and block comments (/* ... */)
// are stripped before checking for the COMMENT keyword so a statement like
// "/* audit */ COMMENT ON COLUMN ..." is still recognized.
func isCommentStatement(stmt string) bool {
	rest, ok := stripLeadingNoise(stmt)
	if !ok {
		return false
	}
	// First real token - check for COMMENT keyword followed by whitespace or end.
	const keyword = "COMMENT"
	if len(rest) < len(keyword) || !strings.EqualFold(rest[:len(keyword)], keyword) {
		return false
	}
	if len(rest) == len(keyword) {
		return true
	}
	next := rest[len(keyword)]
	return next == ' ' || next == '\t' || next == '\n' || next == '\r'
}

// stripLeadingNoise removes leading whitespace, SQL line comments (-- ...),
// and block comments (/* ... */) from a SQL fragment. Returns the remaining
// text and true on success, or "" and false if the fragment contains only
// noise or an unterminated block comment.
func stripLeadingNoise(s string) (string, bool) {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return "", false
		}
		if strings.HasPrefix(s, "--") {
			if nl := strings.IndexByte(s, '\n'); nl >= 0 {
				s = s[nl+1:]
				continue
			}
			return "", false
		}
		if strings.HasPrefix(s, "/*") {
			end := strings.Index(s[2:], "*/")
			if end < 0 {
				return "", false // unterminated block comment
			}
			s = s[2+end+2:]
			continue
		}
		return s, true
	}
}

// splitSQLStatements splits SQL content into individual statements.
// Handles semicolons inside single quotes and comments.
// CockroachDB requires statements to be executed individually.
//
//nolint:gocognit,gocyclo // State machine for SQL parsing necessarily has multiple conditions
func splitSQLStatements(sql string) []string {
	var statements []string
	var current strings.Builder
	inString := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(sql); i++ {
		c := sql[i]

		// Handle line comments
		if !inString && !inBlockComment && i+1 < len(sql) && c == '-' && sql[i+1] == '-' {
			inLineComment = true
		}
		if inLineComment && c == '\n' {
			inLineComment = false
		}

		// Handle block comments
		if !inString && !inLineComment && i+1 < len(sql) && c == '/' && sql[i+1] == '*' {
			inBlockComment = true
		}
		if inBlockComment && i+1 < len(sql) && c == '*' && sql[i+1] == '/' {
			current.WriteByte(c)
			current.WriteByte(sql[i+1])
			i++
			inBlockComment = false
			continue
		}

		// Handle string literals (single quotes)
		if !inLineComment && !inBlockComment && c == '\'' {
			// Check for escaped quote ''
			if i+1 < len(sql) && sql[i+1] == '\'' {
				current.WriteByte(c)
				current.WriteByte(sql[i+1])
				i++
				continue
			}
			inString = !inString
		}

		// Split on semicolon outside strings and comments
		if c == ';' && !inString && !inLineComment && !inBlockComment {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}

		current.WriteByte(c)
	}

	// Add final statement if any
	stmt := strings.TrimSpace(current.String())
	if stmt != "" {
		statements = append(statements, stmt)
	}

	return statements
}
