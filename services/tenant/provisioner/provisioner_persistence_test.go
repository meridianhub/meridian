package provisioner

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMinimalProvisioner creates a PostgresProvisioner with only config set,
// for testing pure conversion functions that do not require a database connection.
func newMinimalProvisioner(services []ServiceConfig) *PostgresProvisioner {
	return &PostgresProvisioner{
		config: &Config{
			Services:            services,
			ProvisioningTimeout: 30 * time.Second,
		},
	}
}

func TestProvisioningEntity_TableName(t *testing.T) {
	entity := provisioningEntity{}
	assert.Equal(t, "tenant_provisioning", entity.TableName())
}

func TestPostgresProvisioner_EntityToStatus_MinimalEntity(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("acme_bank")

	now := time.Now().Truncate(time.Second)
	entity := &provisioningEntity{
		TenantID:  tenantID.String(),
		State:     string(StateActive),
		CreatedAt: now,
		UpdatedAt: now,
	}

	status, err := p.entityToStatus(tenantID, entity)
	require.NoError(t, err)

	assert.Equal(t, tenantID, status.TenantID)
	assert.Equal(t, StateActive, status.State)
	assert.Empty(t, status.ErrorMessage)
	assert.Nil(t, status.DeprovisionedAt)
	assert.Empty(t, status.Services)
}

func TestPostgresProvisioner_EntityToStatus_WithErrorMessage(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("failed_tenant")

	errMsg := "database connection refused"
	entity := &provisioningEntity{
		TenantID:     tenantID.String(),
		State:        string(StateFailed),
		ErrorMessage: &errMsg,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	status, err := p.entityToStatus(tenantID, entity)
	require.NoError(t, err)

	assert.Equal(t, StateFailed, status.State)
	assert.Equal(t, errMsg, status.ErrorMessage)
}

func TestPostgresProvisioner_EntityToStatus_WithDeprovisionedAt(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("deprovisioned_tenant")

	deprovisionedAt := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	entity := &provisioningEntity{
		TenantID:        tenantID.String(),
		State:           string(StateDeprovisioned),
		DeprovisionedAt: &deprovisionedAt,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	status, err := p.entityToStatus(tenantID, entity)
	require.NoError(t, err)

	assert.Equal(t, StateDeprovisioned, status.State)
	require.NotNil(t, status.DeprovisionedAt)
	assert.Equal(t, deprovisionedAt, *status.DeprovisionedAt)
}

func TestPostgresProvisioner_EntityToStatus_WithServices(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("test_tenant")

	entity := &provisioningEntity{
		TenantID:       tenantID.String(),
		State:          string(StateActive),
		ServiceSchemas: `[{"ServiceName":"party","SchemaName":"org_test_tenant","State":"migrated","MigrationVersion":"20251208","ErrorMessage":""}]`,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	status, err := p.entityToStatus(tenantID, entity)
	require.NoError(t, err)

	require.Len(t, status.Services, 1)
	assert.Equal(t, "party", status.Services[0].ServiceName)
	assert.Equal(t, "org_test_tenant", status.Services[0].SchemaName)
	assert.Equal(t, ServiceStateMigrated, status.Services[0].State)
	assert.Equal(t, "20251208", status.Services[0].MigrationVersion)
}

func TestPostgresProvisioner_EntityToStatus_InvalidJSON(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("bad_tenant")

	entity := &provisioningEntity{
		TenantID:       tenantID.String(),
		State:          string(StateActive),
		ServiceSchemas: `invalid json {{{`,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	_, err := p.entityToStatus(tenantID, entity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal service_schemas")
}

func TestPostgresProvisioner_EntityToStatus_EmptyServiceSchemas(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("empty_services")

	entity := &provisioningEntity{
		TenantID:       tenantID.String(),
		State:          string(StatePending),
		ServiceSchemas: "",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	status, err := p.entityToStatus(tenantID, entity)
	require.NoError(t, err)
	assert.Empty(t, status.Services)
}

func TestPostgresProvisioner_EntityToStatus_EmptyArrayServiceSchemas(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("empty_array")

	entity := &provisioningEntity{
		TenantID:       tenantID.String(),
		State:          string(StatePending),
		ServiceSchemas: "[]",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	status, err := p.entityToStatus(tenantID, entity)
	require.NoError(t, err)
	assert.Empty(t, status.Services)
}

func TestPostgresProvisioner_StatusToEntity_MinimalStatus(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("acme_bank")

	now := time.Now().Truncate(time.Second)
	status := &ProvisioningStatus{
		TenantID:  tenantID,
		State:     StateActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	entity, err := p.statusToEntity(status)
	require.NoError(t, err)

	assert.Equal(t, tenantID.String(), entity.TenantID)
	assert.Equal(t, string(StateActive), entity.State)
	assert.Nil(t, entity.ErrorMessage)
	assert.Nil(t, entity.DeprovisionedAt)
}

func TestPostgresProvisioner_StatusToEntity_WithErrorMessage(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("failed_tenant")

	status := &ProvisioningStatus{
		TenantID:     tenantID,
		State:        StateFailed,
		ErrorMessage: "migration script failed",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	entity, err := p.statusToEntity(status)
	require.NoError(t, err)

	require.NotNil(t, entity.ErrorMessage)
	assert.Equal(t, "migration script failed", *entity.ErrorMessage)
}

func TestPostgresProvisioner_StatusToEntity_WithDeprovisionedAt(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("deprovisioned_tenant")

	deprovAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	status := &ProvisioningStatus{
		TenantID:        tenantID,
		State:           StateDeprovisioned,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		DeprovisionedAt: &deprovAt,
	}

	entity, err := p.statusToEntity(status)
	require.NoError(t, err)

	require.NotNil(t, entity.DeprovisionedAt)
	assert.Equal(t, deprovAt, *entity.DeprovisionedAt)
}

func TestPostgresProvisioner_StatusToEntity_WithServices(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("test_tenant")

	status := &ProvisioningStatus{
		TenantID: tenantID,
		State:    StateActive,
		Services: []ServiceSchemaStatus{
			{
				ServiceName:      "party",
				SchemaName:       "org_test_tenant",
				State:            ServiceStateMigrated,
				MigrationVersion: "20251208",
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	entity, err := p.statusToEntity(status)
	require.NoError(t, err)

	var decoded []ServiceSchemaStatus
	require.NoError(t, json.Unmarshal([]byte(entity.ServiceSchemas), &decoded))
	require.Len(t, decoded, 1)
	assert.Equal(t, "party", decoded[0].ServiceName)
	assert.Equal(t, "org_test_tenant", decoded[0].SchemaName)
	assert.Equal(t, ServiceStateMigrated, decoded[0].State)
	assert.Equal(t, "20251208", decoded[0].MigrationVersion)
}

func TestPostgresProvisioner_StatusToEntity_RoundTrip(t *testing.T) {
	p := newMinimalProvisioner(nil)
	tenantID := tenant.MustNewTenantID("roundtrip_tenant")

	now := time.Now().Truncate(time.Millisecond)
	errMsg := "test error"
	original := &ProvisioningStatus{
		TenantID:     tenantID,
		State:        StateFailed,
		ErrorMessage: errMsg,
		Services: []ServiceSchemaStatus{
			{
				ServiceName:      "party",
				SchemaName:       "org_roundtrip_tenant",
				State:            ServiceStateFailed,
				MigrationVersion: "20251208",
				ErrorMessage:     "failed",
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	entity, err := p.statusToEntity(original)
	require.NoError(t, err)

	restored, err := p.entityToStatus(tenantID, entity)
	require.NoError(t, err)

	assert.Equal(t, original.TenantID, restored.TenantID)
	assert.Equal(t, original.State, restored.State)
	assert.Equal(t, original.ErrorMessage, restored.ErrorMessage)
	require.Len(t, restored.Services, 1)
	assert.Equal(t, original.Services[0].ServiceName, restored.Services[0].ServiceName)
	assert.Equal(t, original.Services[0].SchemaName, restored.Services[0].SchemaName)
	assert.Equal(t, original.Services[0].State, restored.Services[0].State)
	assert.Equal(t, original.Services[0].MigrationVersion, restored.Services[0].MigrationVersion)
	assert.Equal(t, original.Services[0].ErrorMessage, restored.Services[0].ErrorMessage)
}

func TestPostgresProvisioner_CreateInitialServiceStatuses(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "/path/party"},
		{Name: "current-account", MigrationPath: "/path/current-account"},
		{Name: "position-keeping", MigrationPath: "/path/position-keeping"},
	}
	p := newMinimalProvisioner(services)
	tenantID := tenant.MustNewTenantID("acme_bank")

	statuses := p.createInitialServiceStatuses(tenantID)

	require.Len(t, statuses, 3)

	expectedSchema := tenantID.SchemaName()
	for _, svc := range statuses {
		assert.Equal(t, expectedSchema, svc.SchemaName)
		assert.Equal(t, ServiceStatePending, svc.State)
	}

	assert.Equal(t, "party", statuses[0].ServiceName)
	assert.Equal(t, "current-account", statuses[1].ServiceName)
	assert.Equal(t, "position-keeping", statuses[2].ServiceName)
}

func TestPostgresProvisioner_CreateInitialServiceStatuses_PreservesOrder(t *testing.T) {
	services := []ServiceConfig{
		{Name: "svc-z", MigrationPath: "/z"},
		{Name: "svc-a", MigrationPath: "/a"},
		{Name: "svc-m", MigrationPath: "/m"},
	}
	p := newMinimalProvisioner(services)
	tenantID := tenant.MustNewTenantID("order_tenant")

	statuses := p.createInitialServiceStatuses(tenantID)

	require.Len(t, statuses, 3)
	assert.Equal(t, "svc-z", statuses[0].ServiceName)
	assert.Equal(t, "svc-a", statuses[1].ServiceName)
	assert.Equal(t, "svc-m", statuses[2].ServiceName)
}

func TestPostgresProvisioner_CreateInitialServiceStatuses_EmptyServices(t *testing.T) {
	p := newMinimalProvisioner([]ServiceConfig{})
	tenantID := tenant.MustNewTenantID("empty_tenant")

	statuses := p.createInitialServiceStatuses(tenantID)
	assert.Empty(t, statuses)
}

func TestPostgresProvisioner_CreateInitialServiceStatuses_SchemaName(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "/path"},
	}
	p := newMinimalProvisioner(services)
	tenantID := tenant.MustNewTenantID("beta_corp")

	statuses := p.createInitialServiceStatuses(tenantID)

	require.Len(t, statuses, 1)
	assert.Equal(t, "org_beta_corp", statuses[0].SchemaName)
}
