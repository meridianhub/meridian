package saga

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantMigrationSummary_Counts(t *testing.T) {
	t.Run("empty results", func(t *testing.T) {
		s := &TenantMigrationSummary{}
		m, sk, wm := s.Counts()
		assert.Equal(t, 0, m)
		assert.Equal(t, 0, sk)
		assert.Equal(t, 0, wm)
	})

	t.Run("mixed actions", func(t *testing.T) {
		s := &TenantMigrationSummary{
			Results: []MigrationResult{
				{Action: MigrationActionMigrated},
				{Action: MigrationActionMigrated},
				{Action: MigrationActionSkipped},
				{Action: MigrationActionWouldMigrate},
				{Action: MigrationActionWouldMigrate},
				{Action: MigrationActionWouldMigrate},
				{Action: "unknown_action"},
			},
		}
		m, sk, wm := s.Counts()
		assert.Equal(t, 2, m)
		assert.Equal(t, 1, sk)
		assert.Equal(t, 3, wm)
	})
}

func TestFormatReport(t *testing.T) {
	refID := uuid.New()

	t.Run("dry run report", func(t *testing.T) {
		summaries := []TenantMigrationSummary{
			{
				TenantID: "tenant-1",
				Results: []MigrationResult{
					{Action: MigrationActionWouldMigrate, SagaName: "payment.initiate", Reason: "95% similar", PlatformRefID: &refID},
					{Action: MigrationActionSkipped, SagaName: "custom.workflow", Reason: "no match"},
				},
			},
		}
		report := FormatReport(summaries, true)
		assert.Contains(t, report, "DRY RUN")
		assert.Contains(t, report, "tenant-1")
		assert.Contains(t, report, "payment.initiate")
		assert.Contains(t, report, "Would migrate: 1")
		assert.Contains(t, report, "Skipped: 1")
		assert.Contains(t, report, "platform_ref=")
	})

	t.Run("apply report with error", func(t *testing.T) {
		summaries := []TenantMigrationSummary{
			{
				TenantID: "tenant-ok",
				Results: []MigrationResult{
					{Action: MigrationActionMigrated, SagaName: "payment.settle", Reason: "matched"},
				},
			},
			{
				TenantID: "tenant-fail",
				Error:    errors.New("db connection lost"),
			},
		}
		report := FormatReport(summaries, false)
		assert.NotContains(t, report, "DRY RUN")
		assert.Contains(t, report, "Migrated: 1")
		assert.Contains(t, report, "Errors: 1")
		assert.Contains(t, report, "db connection lost")
	})

	t.Run("empty summaries", func(t *testing.T) {
		report := FormatReport(nil, false)
		assert.Contains(t, report, "Tenants processed: 0")
	})
}

func TestErrPartialMigrationFailure(t *testing.T) {
	err := ErrPartialMigrationFailure
	require.Error(t, err)
	assert.Equal(t, "some tenant migrations failed", err.Error())
}
