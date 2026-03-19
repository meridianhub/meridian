package provisioner

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
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

	tenantID := tenant.MustNewTenantID("acme_bank")

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

	tenantID := tenant.MustNewTenantID("beta_corp")

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

	tenantID := tenant.MustNewTenantID("failing_tenant")

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

func TestSchemaProvisioner_ProvisionSchemas_RetryAfterFailure(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("retry_tenant")

	// Configure initial failure
	provisioner.FailProvisioningFor[tenantID.String()] = ErrTestDatabaseConnectionFailed

	// First attempt fails
	err := provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.Error(t, err)

	status, err := provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, status.State)

	// Remove failure configuration to simulate issue resolved
	delete(provisioner.FailProvisioningFor, tenantID.String())

	// Retry should succeed
	err = provisioner.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify status is now active
	status, err = provisioner.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StateActive, status.State)
}

func TestSchemaProvisioner_ProvisionSchemas_ConcurrentAttemptBlocked(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("concurrent_tenant")

	// Manually set status to in_progress to simulate concurrent attempt
	provisioner.SetStatus(&ProvisioningStatus{
		TenantID:  tenantID,
		State:     StateInProgress,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})

	// Attempt to provision while already in progress
	err := provisioner.ProvisionSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningInProgress)
}

func TestSchemaProvisioner_DeprovisionSchemas_SoftDelete(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("deprovisioned_tenant")

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

	tenantID := tenant.MustNewTenantID("idempotent_deprov")

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

	tenantID := tenant.MustNewTenantID("never_existed")

	// Deprovision non-existent tenant should fail
	err := provisioner.DeprovisionSchemas(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningStatusNotFound)
}

func TestSchemaProvisioner_PurgeSchemas(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("purge_tenant")

	// Provision then deprovision
	_ = provisioner.ProvisionSchemas(context.Background(), tenantID)
	_ = provisioner.DeprovisionSchemas(context.Background(), tenantID)

	// Purge should succeed (no retention period configured)
	err := provisioner.PurgeSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Verify purge was recorded
	assert.Len(t, provisioner.PurgeCalls, 1)

	// Verify status record is removed after purge
	_, err = provisioner.GetProvisioningStatus(context.Background(), tenantID)
	assert.ErrorIs(t, err, ErrProvisioningStatusNotFound)
}

func TestSchemaProvisioner_PurgeSchemas_NotDeprovisioned(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("active_tenant")

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

	tenantID := tenant.MustNewTenantID("retention_tenant")

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

	tenantID := tenant.MustNewTenantID("unknown")

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

	tenantID := tenant.MustNewTenantID("slow_tenant")

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

	assert.Len(t, config.Services, 7)
	assert.Equal(t, 30*time.Second, config.ProvisioningTimeout)
	assert.Equal(t, 7*365*24*time.Hour, config.DataRetentionPeriod) // 7 years

	// Default config should be valid
	require.NoError(t, config.Validate())

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
	assert.Contains(t, serviceNames, "market-information")
	assert.Contains(t, serviceNames, "reference-data")
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr error
	}{
		{
			name:    "empty services",
			config:  &Config{Services: nil, ProvisioningTimeout: time.Second},
			wantErr: ErrNoServicesConfigured,
		},
		{
			name:    "zero timeout",
			config:  &Config{Services: []ServiceConfig{{Name: "test", MigrationPath: "/path"}}, ProvisioningTimeout: 0},
			wantErr: ErrInvalidProvisioningTimeout,
		},
		{
			name:    "negative retention",
			config:  &Config{Services: []ServiceConfig{{Name: "test", MigrationPath: "/path"}}, ProvisioningTimeout: time.Second, DataRetentionPeriod: -1},
			wantErr: ErrInvalidRetentionPeriod,
		},
		{
			name:    "empty service name",
			config:  &Config{Services: []ServiceConfig{{Name: "", MigrationPath: "/path"}}, ProvisioningTimeout: time.Second},
			wantErr: ErrEmptyServiceName,
		},
		{
			name:    "empty migration path",
			config:  &Config{Services: []ServiceConfig{{Name: "test", MigrationPath: ""}}, ProvisioningTimeout: time.Second},
			wantErr: ErrEmptyMigrationPath,
		},
		{
			name:    "valid config",
			config:  &Config{Services: []ServiceConfig{{Name: "test", MigrationPath: "/path"}}, ProvisioningTimeout: time.Second},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestMockProvisioner_Reset(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	provisioner := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("reset_test")

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

func TestGetServiceDatabaseURL(t *testing.T) {
	t.Run("returns env var when set", func(t *testing.T) {
		t.Setenv("PARTY_DATABASE_URL", "postgres://test:test@localhost:5432/party")
		url := getServiceDatabaseURL("party")
		assert.Equal(t, "postgres://test:test@localhost:5432/party", url)
	})

	t.Run("returns fallback URL when env var not set", func(t *testing.T) {
		url := getServiceDatabaseURL("party")
		assert.Contains(t, url, "meridian_party")
		assert.Contains(t, url, "cockroachdb:26257")
	})

	t.Run("handles hyphens in service name", func(t *testing.T) {
		t.Setenv("CURRENT_ACCOUNT_DATABASE_URL", "postgres://test@localhost/ca")
		url := getServiceDatabaseURL("current-account")
		assert.Equal(t, "postgres://test@localhost/ca", url)
	})

	t.Run("fallback with hyphens replaced", func(t *testing.T) {
		url := getServiceDatabaseURL("current-account")
		assert.Contains(t, url, "meridian_current_account")
	})
}

func TestProvisioningStatus_GetServiceStatus(t *testing.T) {
	status := &ProvisioningStatus{
		Services: []ServiceSchemaStatus{
			{ServiceName: "party", State: ServiceStateMigrated},
			{ServiceName: "account", State: ServiceStatePending},
		},
	}

	t.Run("found", func(t *testing.T) {
		svc := status.getServiceStatus("party")
		require.NotNil(t, svc)
		assert.Equal(t, ServiceStateMigrated, svc.State)
	})

	t.Run("not found", func(t *testing.T) {
		svc := status.getServiceStatus("nonexistent")
		assert.Nil(t, svc)
	})
}

func TestMockProvisioner_ReconcileMigrations_SingleTenant(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "services/party/migrations"},
	}
	prov := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("reconcile_tenant")
	// Provision first to have an active tenant
	err := prov.ProvisionSchemas(context.Background(), tenantID)
	require.NoError(t, err)

	// Reconcile single tenant
	count, errs := prov.ReconcileMigrations(context.Background(), &tenantID)
	assert.Equal(t, 1, count)
	assert.Empty(t, errs)
}

func TestMockProvisioner_ReconcileMigrations_NonActiveTenant(t *testing.T) {
	prov := NewMockProvisioner(nil)

	tenantID := tenant.MustNewTenantID("inactive_tenant")
	// Don't provision - tenant doesn't exist
	count, errs := prov.ReconcileMigrations(context.Background(), &tenantID)
	assert.Equal(t, 0, count)
	assert.Empty(t, errs)
}

func TestMockProvisioner_GetRequiredSchemas(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "/path"},
		{Name: "account", MigrationPath: "/path"},
	}
	prov := NewMockProvisioner(services)

	schemas := prov.GetRequiredSchemas()
	assert.Equal(t, []string{"party", "account"}, schemas)
}

func TestMockProvisioner_GetRequiredSchemas_Empty(t *testing.T) {
	prov := NewMockProvisioner(nil)
	schemas := prov.GetRequiredSchemas()
	assert.Empty(t, schemas)
}

func TestMockProvisioner_InitializeProvisioningStatus(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "/path"},
	}
	prov := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("init_status")
	err := prov.InitializeProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)

	status, err := prov.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, StatePending, status.State)
}

func TestMockProvisioner_InitializeProvisioningStatus_Idempotent(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "/path"},
	}
	prov := NewMockProvisioner(services)

	tenantID := tenant.MustNewTenantID("idempotent_init")

	err := prov.InitializeProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)

	// Second call should be no-op
	err = prov.InitializeProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
}

func TestMockProvisioner_GetProvisioningCallCount(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "/path"},
	}
	prov := NewMockProvisioner(services)

	assert.Equal(t, 0, prov.GetProvisioningCallCount())

	tenantID := tenant.MustNewTenantID("call_count")
	_ = prov.ProvisionSchemas(context.Background(), tenantID)

	assert.Equal(t, 1, prov.GetProvisioningCallCount())
}

func TestMockProvisioner_GetProvisioningCallCountForTenant(t *testing.T) {
	services := []ServiceConfig{
		{Name: "party", MigrationPath: "/path"},
	}
	prov := NewMockProvisioner(services)

	t1 := tenant.MustNewTenantID("tenant_a")
	t2 := tenant.MustNewTenantID("tenant_b")

	_ = prov.ProvisionSchemas(context.Background(), t1)
	_ = prov.ProvisionSchemas(context.Background(), t2)
	_ = prov.ProvisionSchemas(context.Background(), t1) // second call for t1

	assert.Equal(t, 2, prov.GetProvisioningCallCountForTenant("tenant_a"))
	assert.Equal(t, 1, prov.GetProvisioningCallCountForTenant("tenant_b"))
	assert.Equal(t, 0, prov.GetProvisioningCallCountForTenant("tenant_c"))
}

func TestMockProvisioner_ClearFailure(t *testing.T) {
	prov := NewMockProvisioner(nil)
	prov.FailProvisioningFor["tenant_a"] = ErrTestGeneric

	assert.True(t, prov.ClearFailure("tenant_a"))
	assert.False(t, prov.ClearFailure("tenant_a")) // already cleared
	assert.False(t, prov.ClearFailure("tenant_b")) // never existed
}

func TestDefaultConfig_WithCustomBasePath(t *testing.T) {
	t.Setenv("MIGRATIONS_BASE_PATH", "/custom/migrations")
	config := DefaultConfig()

	for _, svc := range config.Services {
		assert.True(t, len(svc.MigrationPath) > 0)
		assert.Contains(t, svc.MigrationPath, "/custom/migrations/")
	}
}

func TestMockProvisioner_SetStatus(t *testing.T) {
	provisioner := NewMockProvisioner(nil)

	tenantID := tenant.MustNewTenantID("manual_status")

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
