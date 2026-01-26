// Package saga provides the admin migration tool for converting existing tenant
// saga definitions from script-copied to platform-referenced model.
//
// Usage:
//
//	migrator := saga.NewPlatformRefMigrator(pool, registry)
//	results, err := migrator.MigrateAllTenants(ctx, tenantIDs, true)  // dry-run
//	results, err := migrator.MigrateAllTenants(ctx, tenantIDs, false) // apply
package saga

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Migration action constants for MigrationResult.Action.
const (
	// MigrationActionMigrated indicates the saga was successfully migrated.
	MigrationActionMigrated = "migrated"
	// MigrationActionSkipped indicates the saga was skipped.
	MigrationActionSkipped = "skipped"
	// MigrationActionWouldMigrate indicates the saga would be migrated (dry-run).
	MigrationActionWouldMigrate = "would_migrate"
)

// PlatformRefMigrator migrates existing tenant saga definitions from
// script-copied to platform-referenced model.
type PlatformRefMigrator struct {
	overrideService *OverrideService
	logger          *slog.Logger
}

// NewPlatformRefMigrator creates a new migration tool.
func NewPlatformRefMigrator(pool *pgxpool.Pool, registry *PostgresRegistry) *PlatformRefMigrator {
	return &PlatformRefMigrator{
		overrideService: NewOverrideService(pool, registry),
		logger:          slog.Default().With("component", "platform_ref_migrator"),
	}
}

// TenantMigrationSummary summarizes the migration results for a single tenant.
type TenantMigrationSummary struct {
	TenantID string
	Results  []MigrationResult
	Error    error
}

// Counts returns the migration action counts.
func (s *TenantMigrationSummary) Counts() (migrated, skipped, wouldMigrate int) {
	for _, r := range s.Results {
		switch r.Action {
		case MigrationActionMigrated:
			migrated++
		case MigrationActionSkipped:
			skipped++
		case MigrationActionWouldMigrate:
			wouldMigrate++
		}
	}
	return migrated, skipped, wouldMigrate
}

// MigrateAllTenants runs the migration for multiple tenants.
// When dryRun is true, reports what would change without making modifications.
func (m *PlatformRefMigrator) MigrateAllTenants(
	ctx context.Context,
	tenantIDs []tenant.TenantID,
	dryRun bool,
) ([]TenantMigrationSummary, error) {
	m.logger.Info("starting bulk platform reference migration",
		"tenant_count", len(tenantIDs),
		"dry_run", dryRun)

	summaries := make([]TenantMigrationSummary, 0, len(tenantIDs))

	for _, tid := range tenantIDs {
		results, err := m.overrideService.MigrateToPlatformRef(ctx, tid, dryRun)
		summary := TenantMigrationSummary{
			TenantID: tid.String(),
			Results:  results,
			Error:    err,
		}
		summaries = append(summaries, summary)

		if err != nil {
			m.logger.Error("migration failed for tenant",
				"tenant_id", tid.String(),
				"error", err)
			continue
		}

		migrated, skipped, wouldMigrate := summary.Counts()
		m.logger.Info("tenant migration completed",
			"tenant_id", tid.String(),
			"migrated", migrated,
			"skipped", skipped,
			"would_migrate", wouldMigrate)
	}

	return summaries, nil
}

// FormatReport generates a human-readable migration report.
func FormatReport(summaries []TenantMigrationSummary, dryRun bool) string {
	var totalMigrated, totalSkipped, totalWouldMigrate, totalErrors int

	var report string
	if dryRun {
		report = "=== Platform Reference Migration Report (DRY RUN) ===\n\n"
	} else {
		report = "=== Platform Reference Migration Report ===\n\n"
	}

	for _, s := range summaries {
		report += fmt.Sprintf("Tenant: %s\n", s.TenantID)

		if s.Error != nil {
			report += fmt.Sprintf("  ERROR: %v\n", s.Error)
			totalErrors++
			continue
		}

		migrated, skipped, wouldMigrate := s.Counts()
		totalMigrated += migrated
		totalSkipped += skipped
		totalWouldMigrate += wouldMigrate

		for _, r := range s.Results {
			report += fmt.Sprintf("  [%s] %s: %s", r.Action, r.SagaName, r.Reason)
			if r.PlatformRefID != nil {
				report += fmt.Sprintf(" (platform_ref=%s)", r.PlatformRefID)
			}
			report += "\n"
		}
		report += "\n"
	}

	report += "=== Summary ===\n"
	report += fmt.Sprintf("Tenants processed: %d\n", len(summaries))
	if dryRun {
		report += fmt.Sprintf("Would migrate: %d\n", totalWouldMigrate)
	} else {
		report += fmt.Sprintf("Migrated: %d\n", totalMigrated)
	}
	report += fmt.Sprintf("Skipped: %d\n", totalSkipped)
	report += fmt.Sprintf("Errors: %d\n", totalErrors)

	return report
}
