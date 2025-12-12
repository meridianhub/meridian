package provisioner

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/organization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test interface usability by exercising the mock provisioner.

func TestSchemaProvisioner_ProvisionSchemas(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
		{Name: "current-account", MigrationPath: "services/current-account/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("acme_bank")

	// Provision schemas for tenant
	err := provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify provisioning was recorded
	assert.Len(t, provisioner.ProvisioningCalls, 1)
	assert.Equal(t, tenantID, provisioner.ProvisioningCalls[0])

	// Verify status is active
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
	assert.Len(t, status.Services, 2)

	// Verify service statuses
	for _, svc := range status.Services {
		assert.Equal(t, ServiceStateMigrated, svc.State)
		assert.Equal(t, "org_acme_bank", svc.SchemaName)
	}
}

func TestSchemaProvisioner_ProvisionSchemas_Idempotent(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("beta_corp")

	// Provision twice
	err := provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Both calls should be recorded (even if second is no-op)
	assert.Len(t, provisioner.ProvisioningCalls, 2)

	// Status should still be active
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestSchemaProvisioner_ProvisionSchemas_Failure(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("failing_tenant")

	// Configure failure using sentinel error
	provisioner.FailProvisioningFor[tenantID.String()] = ErrTestDatabaseConnectionFailed

	// Attempt provisioning
	err := provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTestDatabaseConnectionFailed)

	// Verify status is failed
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)
	assert.Contains(t, status.ErrorMessage, "database connection failed")
}

func TestSchemaProvisioner_DeprovisionSchemas_SoftDelete(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("deprovisioned_tenant")

	// First provision
	err := provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Then deprovision (soft delete)
	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify deprovisioning was recorded
	assert.Len(t, provisioner.DeprovisioningCalls, 1)

	// Verify status still exists but is marked as deprovisioned (soft delete)
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateDeprovisioned, status.State)
	assert.NotNil(t, status.DeprovisionedAt)
}

func TestSchemaProvisioner_DeprovisionSchemas_Idempotent(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("idempotent_deprov")

	// First provision
	err := provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Deprovision twice
	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	err = provisioner.DeprovisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Both calls should be recorded
	assert.Len(t, provisioner.DeprovisioningCalls, 2)

	// Status should still be deprovisioned
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateDeprovisioned, status.State)
}

func TestSchemaProvisioner_DeprovisionSchemas_NotFound(t *testing.T) {
	provisioner := NewMockProvisioner(nil)

	tenantID := organization.MustNewOrganizationID("never_existed")

	// Deprovision non-existent tenant should fail
	err := provisioner.DeprovisionSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningStatusNotFound)
}

func TestSchemaProvisioner_PurgeSchemas(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("purge_tenant")

	// Provision then deprovision
	_ = provisioner.ProvisionSchemas(context.Background(), tenantID)
	_ = provisioner.DeprovisionSchemas(context.Background(), tenantID)

	// Purge should succeed (no retention period configured)
	err := provisioner.PurgeSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify purge was recorded
	assert.Len(t, provisioner.PurgeCalls, 1)
}

func TestSchemaProvisioner_PurgeSchemas_NotDeprovisioned(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("active_tenant")

	// Provision but don't deprovision
	_ = provisioner.ProvisionSchemas(context.Background(), tenantID)

	// Purge should fail - tenant is still active
	err := provisioner.PurgeSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrNotDeprovisioned)
}

func TestSchemaProvisioner_PurgeSchemas_RetentionPeriodNotElapsed(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)
	provisioner.DataRetentionPeriod = 7 * 24 * time.Hour // 7 days

	tenantID := organization.MustNewOrganizationID("retention_tenant")

	// Provision then deprovision
	_ = provisioner.ProvisionSchemas(context.Background(), tenantID)
	_ = provisioner.DeprovisionSchemas(context.Background(), tenantID)

	// Purge should fail - retention period not elapsed
	err := provisioner.PurgeSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrRetentionPeriodNotElapsed)
}

func TestSchemaProvisioner_GetProvisioningStatus_NotFound(t *testing.T) {
	services := []ServiceConfig{}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("unknown")

	_, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningStatusNotFound)
}

func TestSchemaProvisioner_Timeout(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	// Configure delay longer than context timeout
	provisioner.ProvisioningDelay = 500 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	tenantID := organization.MustNewOrganizationID("slow_tenant")

	err := provisioner.ProvisionSchemas(ctx, tenantID)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestProvisioningState_IsValid(t *testing.T) {
	tests := []struct {
		state ProvisioningState
		valid bool
	}{
		{StatePending, true},
		{StateInProgress, true},
		{StateActive, true},
		{StateFailed, true},
		{StateDeprovisioned, true},
		{ProvisioningState("unknown"), false},
		{ProvisioningState(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.state.IsValid())
		})
	}
}

func TestProvisioningState_IsTerminal(t *testing.T) {
	tests := []struct {
		state    ProvisioningState
		terminal bool
	}{
		{StatePending, false},
		{StateInProgress, false},
		{StateActive, true},
		{StateFailed, true},
		{StateDeprovisioned, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.terminal, tt.state.IsTerminal())
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Len(t, config.Services, 5)
	assert.Equal(t, 30*time.Second, config.ProvisioningTimeout)
	assert.Equal(t, 7*365*24*time.Hour, config.DataRetentionPeriod) // 7 years

	// Verify expected services
	serviceNames := make([]string, len(config.Services))
	for i, svc := range config.Services {
		serviceNames[i] = svc.Name
	}
	assert.Contains(t, serviceNames, "party")
	assert.Contains(t, serviceNames, "current-account")
	assert.Contains(t, serviceNames, "position-keeping")
	assert.Contains(t, serviceNames, "financial-accounting")
	assert.Contains(t, serviceNames, "payment-order")
}

func TestMockProvisioner_Reset(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := organization.MustNewOrganizationID("reset_test")

	// Set up some state
	_ = provisioner.ProvisionSchemas(context.Background(), tenantID)
	provisioner.FailProvisioningFor["some_tenant"] = ErrTestGeneric
	provisioner.ProvisioningDelay = time.Second

	// Reset
	provisioner.Reset()

	// Verify all state is cleared
	assert.Empty(t, provisioner.ProvisioningCalls)
	assert.Empty(t, provisioner.DeprovisioningCalls)
	assert.Empty(t, provisioner.FailProvisioningFor)
	assert.Zero(t, provisioner.ProvisioningDelay)

	// Status should no longer exist
	_, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningStatusNotFound)
}

func TestMockProvisioner_SetStatus(t *testing.T) {
	provisioner := NewMockProvisioner(nil)

	tenantID := organization.MustNewOrganizationID("manual_status")

	// Manually set a custom status
	customStatus := &ProvisioningStatus{
		TenantID: tenantID,
		State:    StateInProgress,
		Services: []ServiceSchemaStatus{
			{ServiceName: "custom", SchemaName: "org_manual", State: ServiceStateCreated},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	provisioner.SetStatus(customStatus)

	// Retrieve and verify
	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateInProgress, status.State)
	assert.Len(t, status.Services, 1)
	assert.Equal(t, "custom", status.Services[0].ServiceName)
}
