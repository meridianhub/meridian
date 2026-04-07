package scheduling

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Unit tests for TenantScheduleProvider ---

type mockTenantScheduleRepo struct {
	schedules []TenantSchedule
	err       error
}

func (m *mockTenantScheduleRepo) ListEnabledSchedules(_ context.Context) ([]TenantSchedule, error) {
	return m.schedules, m.err
}

func TestTenantScheduleProvider_ListSchedules_ReturnsSchedules(t *testing.T) {
	meta := `{"key":"value"}`
	repo := &mockTenantScheduleRepo{
		schedules: []TenantSchedule{
			{TenantID: "acme", ScheduleName: "daily-billing", SagaName: "billing.run", CronExpr: "0 0 * * *", Metadata: &meta},
			{TenantID: "beta", ScheduleName: "hourly-sync", SagaName: "sync.run", CronExpr: "0 * * * *"},
		},
	}

	provider := NewTenantScheduleProvider(repo, nil)
	schedules, err := provider.ListSchedules(context.Background())

	require.NoError(t, err)
	require.Len(t, schedules, 2)

	assert.Equal(t, "acme:daily-billing", schedules[0].ID)
	assert.Equal(t, "0 0 * * *", schedules[0].CronExpr)
	assert.Equal(t, "acme", schedules[0].TenantID)
	assert.Equal(t, meta, schedules[0].Metadata)

	assert.Equal(t, "beta:hourly-sync", schedules[1].ID)
	assert.Equal(t, "0 * * * *", schedules[1].CronExpr)
	assert.Equal(t, "beta", schedules[1].TenantID)
	assert.Nil(t, schedules[1].Metadata)
}

func TestTenantScheduleProvider_ListSchedules_EmptyResult(t *testing.T) {
	repo := &mockTenantScheduleRepo{schedules: nil}
	provider := NewTenantScheduleProvider(repo, nil)

	schedules, err := provider.ListSchedules(context.Background())

	require.NoError(t, err)
	assert.Empty(t, schedules)
}

func TestTenantScheduleProvider_ListSchedules_PropagatesError(t *testing.T) {
	repoErr := errors.New("db connection failed")
	repo := &mockTenantScheduleRepo{err: repoErr}
	provider := NewTenantScheduleProvider(repo, nil)

	_, err := provider.ListSchedules(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, repoErr)
}

func TestTenantScheduleProvider_ScheduleIDFormat(t *testing.T) {
	repo := &mockTenantScheduleRepo{
		schedules: []TenantSchedule{
			{TenantID: "my_tenant", ScheduleName: "my-schedule", CronExpr: "0 0 * * *"},
		},
	}
	provider := NewTenantScheduleProvider(repo, nil)

	schedules, err := provider.ListSchedules(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "my_tenant:my-schedule", schedules[0].ID)
}

func TestSchemaToTenantID(t *testing.T) {
	assert.Equal(t, "acme", schemaToTenantID("org_acme"))
	assert.Equal(t, "beta_corp", schemaToTenantID("org_beta_corp"))
	assert.Equal(t, "plain", schemaToTenantID("plain"))
}

// --- Integration tests for GormTenantScheduleRepository ---

const tenantScheduleDDL = `CREATE TABLE IF NOT EXISTS %s.tenant_schedule (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	schedule_name VARCHAR(128) NOT NULL,
	saga_name VARCHAR(128) NOT NULL,
	cron_expr VARCHAR(64) NOT NULL,
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	manifest_version_id UUID,
	metadata JSONB,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE (schedule_name)
)`

func setupTestDB(t *testing.T) (*testdb.TenantTestContext, *testdb.TenantTestContext, func()) {
	t.Helper()

	db, cleanup := testdb.SetupCockroachDB(t, nil)

	tc1 := testdb.SetupTenantSchema(t, db, "alpha")
	testdb.CreateTable(t, tc1.DB, tc1.Tenant, tenantScheduleDDL)

	tc2 := testdb.SetupTenantSchema(t, db, "beta")
	testdb.CreateTable(t, tc2.DB, tc2.Tenant, tenantScheduleDDL)

	// Reset search_path so cross-schema queries work
	require.NoError(t, db.Exec("SET search_path TO public").Error)

	return tc1, tc2, cleanup
}

func TestGormTenantScheduleRepository_ListEnabledSchedules_MultiTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc1, tc2, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert enabled schedule in alpha
	require.NoError(t, tc1.DB.Exec(fmt.Sprintf(`INSERT INTO %q.tenant_schedule (schedule_name, saga_name, cron_expr, enabled) VALUES ('daily-billing', 'billing.run', '0 0 * * *', true)`, tc1.Tenant.SchemaName())).Error)

	// Insert enabled + disabled schedules in beta
	require.NoError(t, tc2.DB.Exec(fmt.Sprintf(`INSERT INTO %q.tenant_schedule (schedule_name, saga_name, cron_expr, enabled) VALUES ('hourly-sync', 'sync.run', '0 * * * *', true)`, tc2.Tenant.SchemaName())).Error)
	require.NoError(t, tc2.DB.Exec(fmt.Sprintf(`INSERT INTO %q.tenant_schedule (schedule_name, saga_name, cron_expr, enabled) VALUES ('disabled-job', 'noop.run', '0 0 * * *', false)`, tc2.Tenant.SchemaName())).Error)

	repo := NewGormTenantScheduleRepository(tc1.DB, nil)
	schedules, err := repo.ListEnabledSchedules(context.Background())

	require.NoError(t, err)
	require.Len(t, schedules, 2, "should return enabled schedules from both tenants")

	// Verify tenant IDs and schedule names
	byKey := make(map[string]TenantSchedule)
	for _, s := range schedules {
		byKey[s.TenantID+":"+s.ScheduleName] = s
	}

	alpha, ok := byKey["alpha:daily-billing"]
	require.True(t, ok, "should contain alpha's daily-billing schedule")
	assert.Equal(t, "billing.run", alpha.SagaName)
	assert.Equal(t, "0 0 * * *", alpha.CronExpr)

	beta, ok := byKey["beta:hourly-sync"]
	require.True(t, ok, "should contain beta's hourly-sync schedule")
	assert.Equal(t, "sync.run", beta.SagaName)
	assert.Equal(t, "0 * * * *", beta.CronExpr)
}

func TestGormTenantScheduleRepository_ListEnabledSchedules_OnlyEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc1, _, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert one enabled and one disabled
	require.NoError(t, tc1.DB.Exec(fmt.Sprintf(`INSERT INTO %q.tenant_schedule (schedule_name, saga_name, cron_expr, enabled) VALUES ('enabled-job', 'job.run', '0 0 * * *', true)`, tc1.Tenant.SchemaName())).Error)
	require.NoError(t, tc1.DB.Exec(fmt.Sprintf(`INSERT INTO %q.tenant_schedule (schedule_name, saga_name, cron_expr, enabled) VALUES ('disabled-job', 'noop.run', '0 0 * * *', false)`, tc1.Tenant.SchemaName())).Error)

	repo := NewGormTenantScheduleRepository(tc1.DB, nil)
	schedules, err := repo.ListEnabledSchedules(context.Background())

	require.NoError(t, err)

	// Filter to alpha schedules and verify only enabled-job is returned
	var alphaSchedules []TenantSchedule
	for _, s := range schedules {
		if s.TenantID == "alpha" {
			alphaSchedules = append(alphaSchedules, s)
		}
	}
	require.Len(t, alphaSchedules, 1, "should return exactly one enabled schedule from alpha")
	assert.Equal(t, "enabled-job", alphaSchedules[0].ScheduleName)
}

func TestGormTenantScheduleRepository_ListEnabledSchedules_EmptyTenants(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc1, _, cleanup := setupTestDB(t)
	defer cleanup()

	// No schedules inserted - tenant tables exist but are empty
	repo := NewGormTenantScheduleRepository(tc1.DB, nil)
	schedules, err := repo.ListEnabledSchedules(context.Background())

	require.NoError(t, err)
	assert.Empty(t, schedules)
}
