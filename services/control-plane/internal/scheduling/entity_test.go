package scheduling

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestTenantScheduleEntity_TableName(t *testing.T) {
	e := TenantScheduleEntity{}
	assert.Equal(t, "tenant_schedule", e.TableName())
}

func TestTenantScheduleEntity_Fields(t *testing.T) {
	id := uuid.New()
	versionID := uuid.New()
	meta := `{"key":"value"}`
	now := time.Now()

	e := TenantScheduleEntity{
		ID:                id,
		ScheduleName:      "daily-billing",
		SagaName:          "billing.run_daily",
		CronExpr:          "0 0 * * *",
		Enabled:           true,
		ManifestVersionID: &versionID,
		Metadata:          &meta,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	assert.Equal(t, id, e.ID)
	assert.Equal(t, "daily-billing", e.ScheduleName)
	assert.Equal(t, "billing.run_daily", e.SagaName)
	assert.Equal(t, "0 0 * * *", e.CronExpr)
	assert.True(t, e.Enabled)
	assert.Equal(t, &versionID, e.ManifestVersionID)
	assert.Equal(t, &meta, e.Metadata)
}
