// Package migrations provides an embedded migration runner that applies
// SQL migration files from an embed.FS to CockroachDB databases.
//
// It handles database and user provisioning, migration tracking via
// a _meridian_migrations table, and idempotent re-runs.
package migrations

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrUnknownService is returned when a migration file belongs to a service
// that has no entry in ServiceDatabases.
var ErrUnknownService = errors.New("unknown service: no database mapping")

// ServiceDatabase maps a service directory name to its target database, user, and password.
type ServiceDatabase struct {
	Database string
	User     string
	Password string
}

// ServiceDatabases defines the mapping from service directory names to
// CockroachDB database names, users, and passwords.
//
// Two services (tenant, control-plane) share meridian_platform.
// Their migrations are applied in service-name order (control-plane before tenant).
var ServiceDatabases = map[string]ServiceDatabase{
	"control-plane":         {Database: "meridian_platform", User: "meridian_platform_user", Password: ""},
	"tenant":                {Database: "meridian_platform", User: "meridian_platform_user", Password: ""},
	"current-account":       {Database: "meridian_current_account", User: "meridian_current_account_user", Password: ""},
	"financial-accounting":  {Database: "meridian_financial_accounting", User: "meridian_financial_accounting_user", Password: ""},
	"position-keeping":      {Database: "meridian_position_keeping", User: "meridian_position_keeping_user", Password: ""},
	"payment-order":         {Database: "meridian_payment_order", User: "meridian_payment_order_user", Password: ""},
	"party":                 {Database: "meridian_party", User: "meridian_party_user", Password: ""},
	"internal-bank-account": {Database: "meridian_internal_bank_account", User: "meridian_internal_bank_account_user", Password: ""},
	"market-information":    {Database: "meridian_market_information", User: "meridian_market_information_user", Password: ""},
	"reconciliation":        {Database: "meridian_reconciliation", User: "meridian_reconciliation_user", Password: ""},
	"forecasting":           {Database: "meridian_forecasting", User: "meridian_forecasting_user", Password: ""},
	"reference-data":        {Database: "meridian_reference_data", User: "meridian_reference_data_user", Password: ""},
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
// The superuserDSN should connect to CockroachDB as a privileged user (e.g., root)
// capable of CREATE DATABASE, CREATE USER, and GRANT operations.
//
// Migration state is tracked per-database in a _meridian_migrations table.
// Running this function multiple times is safe (idempotent).
func RunMigrations(ctx context.Context, migrationFS fs.FS, superuserDSN string, logger *slog.Logger) error {
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
	if err := provisionAll(ctx, superuserDSN, dbSet, logger); err != nil {
		return err
	}

	// Group migrations by target database.
	byDB := groupByDatabase(migrations)

	// Apply migrations to each database.
	for dbName, dbMigrations := range byDB {
		sdb := dbMigrations[0].sdb
		dsn := buildServiceDSN(superuserDSN, sdb)

		if err := applyDatabaseMigrations(ctx, dsn, dbName, dbMigrations, logger); err != nil {
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
func provisionAll(ctx context.Context, superuserDSN string, databases map[string]ServiceDatabase, logger *slog.Logger) error {
	superConn, err := pgx.Connect(ctx, superuserDSN)
	if err != nil {
		return fmt.Errorf("connect as superuser: %w", err)
	}
	defer func() { _ = superConn.Close(ctx) }()

	return provisionDatabases(ctx, superConn, databases, logger)
}

// provisionDatabases creates databases and users as needed.
// Each DDL statement is executed individually because pgx v5's extended
// protocol does not support multi-statement query strings.
func provisionDatabases(ctx context.Context, conn *pgx.Conn, databases map[string]ServiceDatabase, logger *slog.Logger) error {
	for dbName, sdb := range databases {
		logger.Info("provisioning database", "database", dbName, "user", sdb.User)

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
func applyDatabaseMigrations(ctx context.Context, dsn, dbName string, migrations []dbMigration, logger *slog.Logger) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", dbName, err)
	}
	defer func() { _ = conn.Close(ctx) }()

	if err := ensureMigrationsTable(ctx, conn); err != nil {
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

		if _, err := conn.Exec(ctx, m.sql); err != nil {
			return fmt.Errorf("execute %s/%s: %w", m.service, m.filename, err)
		}

		if err := recordMigration(ctx, conn, m.service, m.filename); err != nil {
			return fmt.Errorf("record %s/%s: %w", m.service, m.filename, err)
		}
	}

	return nil
}

// ensureMigrationsTable creates the _meridian_migrations tracking table if it does not exist.
func ensureMigrationsTable(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _meridian_migrations (
			id          INT8 NOT NULL DEFAULT unique_rowid(),
			service     VARCHAR(255) NOT NULL,
			filename    VARCHAR(255) NOT NULL,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (id),
			UNIQUE (service, filename)
		)
	`)
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
func buildServiceDSN(superuserDSN string, sdb ServiceDatabase) string {
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

	// Ensure default port for CockroachDB if not specified.
	if parsed.Port() == "" {
		parsed.Host = parsed.Hostname() + ":26257"
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
