package scheduling

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"gorm.io/gorm"
)

// TenantScheduleRepository queries enabled schedules from tenant schemas.
type TenantScheduleRepository interface {
	// ListEnabledSchedules returns all enabled tenant schedules across all tenant schemas.
	// Each returned TenantSchedule includes the tenant ID derived from the schema name.
	ListEnabledSchedules(ctx context.Context) ([]TenantSchedule, error)
}

// TenantSchedule is the domain representation of a row from tenant_schedule
// augmented with the owning tenant ID.
type TenantSchedule struct {
	TenantID     string
	ScheduleName string
	SagaName     string
	CronExpr     string
	Metadata     *string
}

// TenantScheduleProvider implements scheduler.ScheduleProvider by querying
// tenant_schedule tables across all tenant schemas.
type TenantScheduleProvider struct {
	repo   TenantScheduleRepository
	logger *slog.Logger
}

// NewTenantScheduleProvider creates a new TenantScheduleProvider.
func NewTenantScheduleProvider(repo TenantScheduleRepository, logger *slog.Logger) *TenantScheduleProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &TenantScheduleProvider{
		repo:   repo,
		logger: logger.With("component", "tenant-schedule-provider"),
	}
}

// ListSchedules implements scheduler.ScheduleProvider. It returns all enabled
// schedules from all tenant schemas, with Schedule.ID formatted as "tenant_id:schedule_name".
func (p *TenantScheduleProvider) ListSchedules(ctx context.Context) ([]scheduler.Schedule, error) {
	tenantSchedules, err := p.repo.ListEnabledSchedules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled tenant schedules: %w", err)
	}

	schedules := make([]scheduler.Schedule, len(tenantSchedules))
	for i, ts := range tenantSchedules {
		var metadata any
		if ts.Metadata != nil {
			metadata = *ts.Metadata
		}

		schedules[i] = scheduler.Schedule{
			ID:       ts.TenantID + ":" + ts.ScheduleName,
			CronExpr: ts.CronExpr,
			TenantID: ts.TenantID,
			Metadata: metadata,
		}
	}

	p.logger.Debug("loaded tenant schedules", "count", len(schedules))
	return schedules, nil
}

// Compile-time check that TenantScheduleProvider implements ScheduleProvider.
var _ scheduler.ScheduleProvider = (*TenantScheduleProvider)(nil)

// GormTenantScheduleRepository implements TenantScheduleRepository using GORM.
// It discovers tenant schemas and queries each for enabled schedules.
type GormTenantScheduleRepository struct {
	db            *gorm.DB
	schemaPattern string
	logger        *slog.Logger
}

// NewGormTenantScheduleRepository creates a new repository that queries tenant_schedule
// tables across all tenant schemas matching the given pattern (default: "org_%").
func NewGormTenantScheduleRepository(db *gorm.DB, logger *slog.Logger) *GormTenantScheduleRepository {
	if logger == nil {
		logger = slog.Default()
	}
	return &GormTenantScheduleRepository{
		db:            db,
		schemaPattern: "org_%",
		logger:        logger.With("component", "tenant-schedule-repository"),
	}
}

// ListEnabledSchedules discovers tenant schemas and queries each for enabled schedules.
func (r *GormTenantScheduleRepository) ListEnabledSchedules(ctx context.Context) ([]TenantSchedule, error) {
	schemas, err := r.findTenantSchemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover tenant schemas: %w", err)
	}

	var allSchedules []TenantSchedule
	for _, schema := range schemas {
		tenantID := schemaToTenantID(schema)

		var entities []TenantScheduleEntity
		err := r.db.WithContext(ctx).
			Table(fmt.Sprintf("%q.tenant_schedule", schema)).
			Where("enabled = ?", true).
			Find(&entities).Error
		if err != nil {
			r.logger.Error("failed to query tenant schedules",
				"schema", schema, "error", err)
			continue
		}

		for _, e := range entities {
			allSchedules = append(allSchedules, TenantSchedule{
				TenantID:     tenantID,
				ScheduleName: e.ScheduleName,
				SagaName:     e.SagaName,
				CronExpr:     e.CronExpr,
				Metadata:     e.Metadata,
			})
		}
	}

	return allSchedules, nil
}

// findTenantSchemas queries for schemas matching the pattern that contain a tenant_schedule table.
func (r *GormTenantScheduleRepository) findTenantSchemas(ctx context.Context) ([]string, error) {
	var schemas []string
	err := r.db.WithContext(ctx).Raw(`
		SELECT DISTINCT s.schema_name
		FROM information_schema.schemata s
		JOIN information_schema.tables t
		  ON t.table_schema = s.schema_name AND t.table_name = 'tenant_schedule'
		WHERE s.schema_name LIKE ?
		ORDER BY s.schema_name
	`, r.schemaPattern).Scan(&schemas).Error
	if err != nil {
		return nil, fmt.Errorf("query tenant schemas: %w", err)
	}
	return schemas, nil
}

// schemaToTenantID converts an "org_xxx" schema name back to the tenant ID "xxx".
func schemaToTenantID(schema string) string {
	return strings.TrimPrefix(schema, "org_")
}
