// Package migrations provides an embedded migration runner that applies
// SQL migration files from an embed.FS to CockroachDB or PostgreSQL databases.
//
// It handles database and user provisioning, migration tracking via
// a _meridian_migrations table, and idempotent re-runs.
//
// The driver is selected via the DB_DRIVER environment variable:
//   - "cockroachdb" (default): connects on port 26257, uses CockroachDB DDL
//   - "postgres": connects on port 5432, uses standard PostgreSQL DDL
package migrations

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Driver identifies the target database engine.
type Driver string

const (
	// DriverCockroachDB is the default driver for CockroachDB.
	DriverCockroachDB Driver = "cockroachdb"
	// DriverPostgres targets standard PostgreSQL 13+.
	DriverPostgres Driver = "postgres"
)

// DriverFromEnv reads DB_DRIVER from the environment, defaulting to CockroachDB.
func DriverFromEnv() Driver {
	switch strings.ToLower(os.Getenv("DB_DRIVER")) {
	case "postgres", "postgresql":
		return DriverPostgres
	default:
		return DriverCockroachDB
	}
}

// ErrUnknownService is returned when a migration file belongs to a service
// that has no entry in ServiceDatabases.
var ErrUnknownService = errors.New("unknown service: no database mapping")

// ErrUnsupportedDriver is returned when an unrecognized Driver value is used.
var ErrUnsupportedDriver = errors.New("unsupported driver")

// ServiceDatabase maps a service directory name to its target database, user, and password.
type ServiceDatabase struct {
	Database string
	User     string
	Password string
}

// ServiceDatabases defines the mapping from service directory names to
// CockroachDB database names, users, and passwords.
//
// Control-plane has its own database (meridian_control_plane).
// Tenant uses meridian_platform.
var ServiceDatabases = map[string]ServiceDatabase{
	"control-plane":        {Database: "meridian_control_plane", User: "meridian_control_plane_user", Password: ""},
	"tenant":               {Database: "meridian_platform", User: "meridian_platform_user", Password: ""},
	"current-account":      {Database: "meridian_current_account", User: "meridian_current_account_user", Password: ""},
	"financial-accounting": {Database: "meridian_financial_accounting", User: "meridian_financial_accounting_user", Password: ""},
	"identity":             {Database: "meridian_identity", User: "meridian_identity_user", Password: ""},
	"position-keeping":     {Database: "meridian_position_keeping", User: "meridian_position_keeping_user", Password: ""},
	"payment-order":        {Database: "meridian_payment_order", User: "meridian_payment_order_user", Password: ""},
	"party":                {Database: "meridian_party", User: "meridian_party_user", Password: ""},
	"internal-account":     {Database: "meridian_internal_account", User: "meridian_internal_account_user", Password: ""},
	"market-information":   {Database: "meridian_market_information", User: "meridian_market_information_user", Password: ""},
	"reconciliation":       {Database: "meridian_reconciliation", User: "meridian_reconciliation_user", Password: ""},
	"forecasting":          {Database: "meridian_forecasting", User: "meridian_forecasting_user", Password: ""},
	"reference-data":       {Database: "meridian_reference_data", User: "meridian_reference_data_user", Password: ""},
	"operational-gateway":  {Database: "meridian_operational_gateway", User: "meridian_operational_gateway_user", Password: ""},
	"financial-gateway":    {Database: "meridian_financial_gateway", User: "meridian_financial_gateway_user", Password: ""},
}

// serviceMigration holds a single migration file for a service.
type serviceMigration struct {
	Service  string
	Filename string
	SQL      string
}

// RunMigrations discovers migration files from the provided embed.FS, provisions
// databases and users as superuser, then applies unapplied migrations in order.
//
// The superuserDSN should connect to the database as a privileged user (e.g., root
// for CockroachDB, postgres for PostgreSQL) capable of CREATE DATABASE, CREATE USER,
// and GRANT operations.
//
// The driver is read from the DB_DRIVER environment variable (default: cockroachdb).
//
// Migration state is tracked per-database in a _meridian_migrations table.
// Running this function multiple times is safe (idempotent).
func RunMigrations(ctx context.Context, migrationFS fs.FS, superuserDSN string, logger *slog.Logger) error {
	driver := DriverFromEnv()
	return runMigrations(ctx, migrationFS, superuserDSN, driver, logger)
}

// runMigrations is the internal implementation, accepting an explicit driver
// to allow testing without environment variable manipulation.
func runMigrations(ctx context.Context, migrationFS fs.FS, superuserDSN string, driver Driver, logger *slog.Logger) error {
	logger.Info("running migrations", "driver", driver)

	migrations, err := discoverMigrations(migrationFS)
	if err != nil {
		return fmt.Errorf("discover migrations: %w", err)
	}

	if len(migrations) == 0 {
		logger.Info("no migrations found")
		return nil
	}

	// Collect unique databases to provision.
	dbSet := make(map[string]ServiceDatabase)
	for _, m := range migrations {
		sdb, ok := ServiceDatabases[m.Service]
		if !ok {
			return fmt.Errorf("service %q: %w", m.Service, ErrUnknownService)
		}
		dbSet[sdb.Database] = sdb
	}

	// Connect as superuser to provision databases and users, then close.
	if err := provisionAll(ctx, superuserDSN, dbSet, driver, logger); err != nil {
		return err
	}

	// PostgreSQL compatibility: run pre-migration fixups before applying migrations.
	// CockroachDB and PostgreSQL handle certain DDL operations differently (e.g.,
	// DROP INDEX CASCADE vs ALTER TABLE DROP CONSTRAINT for unique constraints).
	// These fixups resolve the divergence so the same migration files work on both.
	if driver == DriverPostgres {
		if err := runPostgresPreMigrationFixups(ctx, superuserDSN, driver, logger); err != nil {
			return fmt.Errorf("postgres pre-migration fixups: %w", err)
		}
	}

	// Group migrations by target database.
	byDB := groupByDatabase(migrations)

	// Apply migrations to each database using superuser credentials.
	// Service users may not have password-based auth configured (scram-sha-256),
	// so we preserve the superuser credentials and only change the database name.
	for dbName, dbMigrations := range byDB {
		dsn := buildSuperuserDSN(superuserDSN, dbName, driver)

		if err := applyDatabaseMigrations(ctx, dsn, dbName, dbMigrations, driver, logger); err != nil {
			return fmt.Errorf("apply migrations to %s: %w", dbName, err)
		}
	}

	return nil
}

// discoverMigrations reads migration SQL files from the embedded filesystem.
// It expects paths of the form: <service>/migrations/<filename>.sql
func discoverMigrations(migrationFS fs.FS) ([]serviceMigration, error) {
	var migrations []serviceMigration

	err := fs.WalkDir(migrationFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".sql") {
			return nil
		}

		// Expected path: <service>/migrations/<filename>.sql
		parts := strings.Split(path, "/")
		if len(parts) != 3 || parts[1] != "migrations" {
			return nil
		}

		service := parts[0]
		filename := parts[2]

		content, err := fs.ReadFile(migrationFS, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		migrations = append(migrations, serviceMigration{
			Service:  service,
			Filename: filename,
			SQL:      string(content),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort by service name then filename for deterministic ordering.
	sort.Slice(migrations, func(i, j int) bool {
		if migrations[i].Service != migrations[j].Service {
			return migrations[i].Service < migrations[j].Service
		}
		return migrations[i].Filename < migrations[j].Filename
	})

	return migrations, nil
}

// provisionAll connects as superuser, provisions databases, and closes the connection.
// For PostgreSQL, it also connects to each provisioned database to grant public schema
// privileges to the service user (database-level grants alone are insufficient).
func provisionAll(ctx context.Context, superuserDSN string, databases map[string]ServiceDatabase, driver Driver, logger *slog.Logger) error {
	superConn, err := pgx.Connect(ctx, superuserDSN)
	if err != nil {
		return fmt.Errorf("connect as superuser: %w", err)
	}
	defer func() { _ = superConn.Close(ctx) }()

	if err := provisionDatabases(ctx, superConn, databases, driver, logger); err != nil {
		return err
	}

	// For PostgreSQL, grant public schema privileges per database.
	// This cannot be done from a connection to a different database.
	if driver == DriverPostgres {
		for dbName, sdb := range databases {
			if err := grantPostgresSchemaPrivileges(ctx, superuserDSN, dbName, sdb, logger); err != nil {
				return err
			}
		}
	}

	return nil
}

// grantPostgresSchemaPrivileges connects to the target database as superuser and
// grants CREATE on the public schema to the service user.
func grantPostgresSchemaPrivileges(ctx context.Context, superuserDSN string, dbName string, sdb ServiceDatabase, logger *slog.Logger) error {
	// Build a superuser DSN targeting the specific database (preserve superuser credentials).
	parsed, err := url.Parse(superuserDSN)
	if err != nil {
		return fmt.Errorf("parse superuser DSN: %w", err)
	}
	parsed.Path = "/" + dbName
	if parsed.Port() == "" {
		parsed.Host = parsed.Hostname() + ":5432"
	}
	targetDSN := parsed.String()

	conn, err := pgx.Connect(ctx, targetDSN)
	if err != nil {
		return fmt.Errorf("connect to %s for schema grants: %w", dbName, err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if _, err := conn.Exec(ctx, fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s", quoteIdent(sdb.User))); err != nil {
		return fmt.Errorf("schema grant on %s: %w", dbName, err)
	}
	logger.Debug("granted schema privileges", "database", dbName, "user", sdb.User)
	return nil
}

// provisionDatabases creates databases and users as needed.
// Each DDL statement is executed individually because pgx v5's extended
// protocol does not support multi-statement query strings.
//
// CockroachDB supports CREATE DATABASE IF NOT EXISTS and CREATE USER IF NOT EXISTS.
// PostgreSQL does not have IF NOT EXISTS on CREATE DATABASE, so we attempt the creation
// and ignore SQLSTATE 42P04 (duplicate_database) if it already exists.
func provisionDatabases(ctx context.Context, conn *pgx.Conn, databases map[string]ServiceDatabase, driver Driver, logger *slog.Logger) error {
	for dbName, sdb := range databases {
		logger.Info("provisioning database", "database", dbName, "user", sdb.User, "driver", driver)

		switch driver {
		case DriverPostgres:
			if err := provisionPostgresDatabase(ctx, conn, dbName, sdb, logger); err != nil {
				return err
			}
		case DriverCockroachDB:
			stmts := []string{
				fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", quoteIdent(dbName)),
				fmt.Sprintf("CREATE USER IF NOT EXISTS %s", quoteIdent(sdb.User)),
				fmt.Sprintf("GRANT ALL ON DATABASE %s TO %s", quoteIdent(dbName), quoteIdent(sdb.User)),
			}
			for _, stmt := range stmts {
				if _, err := conn.Exec(ctx, stmt); err != nil {
					return fmt.Errorf("provision %s: %w", dbName, err)
				}
			}
		default:
			return fmt.Errorf("%w: %q", ErrUnsupportedDriver, driver)
		}
	}
	return nil
}

// provisionPostgresDatabase creates a database and user for PostgreSQL if they do not exist.
//
// PostgreSQL does not support CREATE DATABASE IF NOT EXISTS or CREATE USER IF NOT EXISTS.
// We attempt the creation and ignore idempotency errors:
//   - 42P04 (duplicate_database) for CREATE DATABASE
//   - 42710 (duplicate_object) for CREATE USER
//
// We connect to the target database separately to grant schema privileges,
// because the superuser connection may be to a different database.
func provisionPostgresDatabase(ctx context.Context, superConn *pgx.Conn, dbName string, sdb ServiceDatabase, logger *slog.Logger) error {
	// 1. Create database — tolerate "already exists" (42P04).
	if _, err := superConn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", quoteIdent(dbName))); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42P04" {
			return fmt.Errorf("provision %s (create database): %w", dbName, err)
		}
		logger.Debug("database already exists, skipping creation", "database", dbName)
	}

	// 2. Create user — tolerate "already exists" (42710 = duplicate_object).
	// PostgreSQL has no CREATE USER IF NOT EXISTS syntax.
	if _, err := superConn.Exec(ctx, fmt.Sprintf("CREATE USER %s", quoteIdent(sdb.User))); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42710" {
			return fmt.Errorf("provision %s (create user): %w", dbName, err)
		}
		logger.Debug("user already exists, skipping creation", "user", sdb.User)
	}

	// 3. Grant database-level privileges.
	if _, err := superConn.Exec(ctx, fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s", quoteIdent(dbName), quoteIdent(sdb.User))); err != nil {
		return fmt.Errorf("provision %s (grant db): %w", dbName, err)
	}

	return nil
}

type dbMigration struct {
	sdb      ServiceDatabase
	service  string
	filename string
	sql      string
}

// groupByDatabase groups migrations by their target database name.
// Within each database, migrations are sorted by filename (lexicographic).
func groupByDatabase(migrations []serviceMigration) map[string][]dbMigration {
	result := make(map[string][]dbMigration)

	for _, m := range migrations {
		sdb := ServiceDatabases[m.Service]
		result[sdb.Database] = append(result[sdb.Database], dbMigration{
			sdb:      sdb,
			service:  m.Service,
			filename: m.Filename,
			sql:      m.SQL,
		})
	}

	// Sort each database's migrations by service then filename.
	for _, dbMigs := range result {
		sort.Slice(dbMigs, func(i, j int) bool {
			if dbMigs[i].service != dbMigs[j].service {
				return dbMigs[i].service < dbMigs[j].service
			}
			return dbMigs[i].filename < dbMigs[j].filename
		})
	}

	return result
}

// applyDatabaseMigrations connects to a specific database and applies unapplied migrations.
func applyDatabaseMigrations(ctx context.Context, dsn, dbName string, migrations []dbMigration, driver Driver, logger *slog.Logger) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", dbName, err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := ensureMigrationsTable(ctx, conn, driver); err != nil {
		return fmt.Errorf("create tracking table: %w", err)
	}

	applied, err := getAppliedMigrations(ctx, conn)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}

	for _, m := range migrations {
		key := m.service + "/" + m.filename
		if applied[key] {
			logger.Debug("skipping already applied migration", "database", dbName, "service", m.service, "file", m.filename)
			continue
		}

		logger.Info("applying migration", "database", dbName, "service", m.service, "file", m.filename)

		sql := m.sql
		if driver == DriverPostgres {
			sql = adaptCockroachDDLForPostgres(sql)
		}

		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("execute %s/%s: %w", m.service, m.filename, err)
		}

		if err := recordMigration(ctx, conn, m.service, m.filename); err != nil {
			return fmt.Errorf("record %s/%s: %w", m.service, m.filename, err)
		}
	}

	return nil
}

// ensureMigrationsTable creates the _meridian_migrations tracking table if it does not exist.
//
// CockroachDB uses unique_rowid() for the default ID. PostgreSQL uses BIGSERIAL (a standard
// auto-incrementing integer sequence). Both produce unique INT8 primary keys.
func ensureMigrationsTable(ctx context.Context, conn *pgx.Conn, driver Driver) error {
	var ddl string
	switch driver {
	case DriverPostgres:
		ddl = `
			CREATE TABLE IF NOT EXISTS _meridian_migrations (
				id          BIGSERIAL PRIMARY KEY,
				service     VARCHAR(255) NOT NULL,
				filename    VARCHAR(255) NOT NULL,
				applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
				UNIQUE (service, filename)
			)
		`
	case DriverCockroachDB:
		ddl = `
			CREATE TABLE IF NOT EXISTS _meridian_migrations (
				id          INT8 NOT NULL DEFAULT unique_rowid(),
				service     VARCHAR(255) NOT NULL,
				filename    VARCHAR(255) NOT NULL,
				applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
				PRIMARY KEY (id),
				UNIQUE (service, filename)
			)
		`
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedDriver, driver)
	}
	_, err := conn.Exec(ctx, ddl)
	return err
}

// getAppliedMigrations returns a set of "service/filename" keys for already-applied migrations.
func getAppliedMigrations(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT service, filename FROM _meridian_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var service, filename string
		if err := rows.Scan(&service, &filename); err != nil {
			return nil, err
		}
		applied[service+"/"+filename] = true
	}
	return applied, rows.Err()
}

// recordMigration inserts a record into _meridian_migrations for a successfully applied migration.
func recordMigration(ctx context.Context, conn *pgx.Conn, service, filename string) error {
	_, err := conn.Exec(ctx,
		`INSERT INTO _meridian_migrations (service, filename, applied_at) VALUES ($1, $2, $3)`,
		service, filename, time.Now(),
	)
	return err
}

// buildServiceDSN modifies a superuser DSN to target a specific database and user.
// It parses the URL, replaces user/database, and preserves all query parameters
// (TLS settings, timeouts, etc.). It also sets simple_protocol exec mode so that
// multi-statement migration files can be executed in a single Exec() call.
//
// The default port is 26257 for CockroachDB and 5432 for PostgreSQL.
func buildServiceDSN(superuserDSN string, sdb ServiceDatabase, driver Driver) string {
	parsed, err := url.Parse(superuserDSN)
	if err != nil {
		return superuserDSN
	}

	// Replace user credentials.
	if sdb.Password != "" {
		parsed.User = url.UserPassword(sdb.User, sdb.Password)
	} else {
		parsed.User = url.User(sdb.User)
	}

	// Replace database in path (postgres://user@host:port/database).
	parsed.Path = "/" + sdb.Database

	// Ensure default port if not specified.
	if parsed.Port() == "" {
		defaultPort := "26257"
		if driver == DriverPostgres {
			defaultPort = "5432"
		}
		parsed.Host = parsed.Hostname() + ":" + defaultPort
	}

	// Enable simple protocol so multi-statement migration SQL files work with pgx v5.
	q := parsed.Query()
	if q.Get("default_query_exec_mode") == "" {
		q.Set("default_query_exec_mode", "simple_protocol")
	}
	parsed.RawQuery = q.Encode()

	return parsed.String()
}

// buildSuperuserDSN modifies a superuser DSN to target a specific database while
// preserving the superuser credentials. This is used for applying migrations where
// the service user may not have password-based authentication configured.
//
// The default port is 26257 for CockroachDB and 5432 for PostgreSQL.
func buildSuperuserDSN(superuserDSN string, dbName string, driver Driver) string {
	parsed, err := url.Parse(superuserDSN)
	if err != nil {
		return superuserDSN
	}

	// Preserve superuser credentials, only replace database.
	parsed.Path = "/" + dbName

	// Ensure default port if not specified.
	if parsed.Port() == "" {
		defaultPort := "26257"
		if driver == DriverPostgres {
			defaultPort = "5432"
		}
		parsed.Host = parsed.Hostname() + ":" + defaultPort
	}

	// Enable simple protocol so multi-statement migration SQL files work with pgx v5.
	q := parsed.Query()
	if q.Get("default_query_exec_mode") == "" {
		q.Set("default_query_exec_mode", "simple_protocol")
	}
	parsed.RawQuery = q.Encode()

	return parsed.String()
}

// quoteIdent wraps a SQL identifier in double quotes, escaping any embedded double quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// runPostgresPreMigrationFixups resolves CockroachDB/PostgreSQL DDL divergences
// before migration files are applied. This is only called when driver == DriverPostgres.
//
// Background: CockroachDB uses DROP INDEX CASCADE to remove unique-constraint-backed
// indexes (which also drops the constraint). PostgreSQL requires ALTER TABLE DROP
// CONSTRAINT first, then DROP INDEX. Since CockroachDB v24.1 doesn't support PL/pgSQL
// DO blocks, we can't handle this in SQL migration files. Instead, we run the
// PostgreSQL-specific fixup here at the Go level.
func runPostgresPreMigrationFixups(ctx context.Context, superuserDSN string, driver Driver, logger *slog.Logger) error {
	// Fix: reference-data migration 20260127000001 uses DROP INDEX CASCADE on
	// uq_platform_saga_definition_name. On PostgreSQL, the constraint must be
	// dropped first. This is a no-op if the constraint doesn't exist (already
	// applied or fresh DB before the table is created - ALTER TABLE IF EXISTS
	// handles that).
	dbName := "meridian_reference_data"
	dsn := buildSuperuserDSN(superuserDSN, dbName, driver)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		// Database might not exist yet on first run - that's fine, migrations will create it.
		logger.Debug("skipping pre-migration fixup (database not ready)", "database", dbName, "error", err)
		return nil
	}
	defer func() { _ = conn.Close(ctx) }()

	fixupSQL := `ALTER TABLE IF EXISTS "public"."platform_saga_definition" DROP CONSTRAINT IF EXISTS "uq_platform_saga_definition_name"`
	if _, err := conn.Exec(ctx, fixupSQL); err != nil {
		logger.Warn("pre-migration fixup failed (non-fatal)", "database", dbName, "error", err)
		// Non-fatal: the constraint might not exist, or the table might not exist yet.
		// If the migration truly needs this fixup and it didn't run, the migration itself
		// will fail with a clear error.
	} else {
		logger.Info("pre-migration fixup applied", "database", dbName, "fixup", "drop_saga_unique_constraint")
	}

	return nil
}

// adaptCockroachDDLForPostgres rewrites CockroachDB-specific DDL to work on PostgreSQL.
//
// Migrations are written for CockroachDB (production). When running against
// PostgreSQL (demo, local dev), this function translates known incompatibilities:
//   - DROP INDEX CASCADE for unique constraints -> ALTER TABLE DROP CONSTRAINT
//   - ADD CONSTRAINT [IF NOT EXISTS] CHECK on public-schema tables -> idempotent DO block
//
// This mirrors shared/platform/testdb/pgx.go:adaptCockroachDDLForPostgres.
func adaptCockroachDDLForPostgres(sql string) string {
	// CockroachDB uses DROP INDEX CASCADE for unique constraints;
	// PostgreSQL requires ALTER TABLE DROP CONSTRAINT.
	sql = strings.ReplaceAll(sql,
		`DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE`,
		`ALTER TABLE "public"."platform_saga_definition" DROP CONSTRAINT IF EXISTS "uq_platform_saga_definition_name"`,
	)
	sql = strings.ReplaceAll(sql,
		`DROP INDEX IF EXISTS uq_manifest_version_version CASCADE`,
		`ALTER TABLE manifest_version DROP CONSTRAINT IF EXISTS uq_manifest_version_version`,
	)

	// Wrap ADD CONSTRAINT ... CHECK statements targeting public-schema tables in a
	// DO block that ignores duplicate_object errors. In multi-tenant tests, multiple
	// schemas apply the same migration against the shared public schema.
	re := regexp.MustCompile(`(?s)(ALTER TABLE\s+public\.\S+\s+)ADD CONSTRAINT(?:\s+IF NOT EXISTS)?\s+(\S+\s+CHECK\s*\([^;]+?\));`)
	sql = re.ReplaceAllStringFunc(sql, func(match string) string {
		inner := strings.Replace(match, "ADD CONSTRAINT IF NOT EXISTS", "ADD CONSTRAINT", 1)
		inner = strings.TrimSuffix(strings.TrimSpace(inner), ";")
		return "DO $compat$ BEGIN " + inner + "; EXCEPTION WHEN duplicate_object THEN NULL; END $compat$;"
	})

	return sql
}
