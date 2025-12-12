package provisioner

import (
	"context"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/organization"
)

// MockProvisioner is an in-memory implementation of SchemaProvisioner for testing.
// It simulates schema provisioning without actually creating database schemas.
//
// Thread-safe for concurrent access.
type MockProvisioner struct {
	mu sync.RWMutex

	// statuses stores provisioning status by tenant ID
	statuses map[string]*ProvisioningStatus

	// services is the list of services to simulate provisioning for
	services []ServiceConfig

	// ProvisioningDelay simulates time spent provisioning (for timeout testing)
	ProvisioningDelay time.Duration

	// FailProvisioningFor lists tenant IDs that should fail during provisioning
	FailProvisioningFor map[string]error

	// FailDeprovisioningFor lists tenant IDs that should fail during deprovisioning
	FailDeprovisioningFor map[string]error

	// ProvisioningCalls tracks calls to ProvisionSchemas for verification
	ProvisioningCalls []organization.OrganizationID

	// DeprovisioningCalls tracks calls to DeprovisionSchemas for verification
	DeprovisioningCalls []organization.OrganizationID
}

// NewMockProvisioner creates a new mock provisioner with the given service configuration.
func NewMockProvisioner(services []ServiceConfig) *MockProvisioner {
	return &MockProvisioner{
		statuses:              make(map[string]*ProvisioningStatus),
		services:              services,
		FailProvisioningFor:   make(map[string]error),
		FailDeprovisioningFor: make(map[string]error),
		ProvisioningCalls:     make([]organization.OrganizationID, 0),
		DeprovisioningCalls:   make([]organization.OrganizationID, 0),
	}
}

// ProvisionSchemas simulates schema provisioning for the tenant.
func (m *MockProvisioner) ProvisionSchemas(ctx context.Context, tenantID organization.OrganizationID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the call
	m.ProvisioningCalls = append(m.ProvisioningCalls, tenantID)

	// Simulate delay if configured
	if m.ProvisioningDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.ProvisioningDelay):
		}
	}

	// Check for simulated failure
	if err, shouldFail := m.FailProvisioningFor[tenantID.String()]; shouldFail {
		// Create failed status
		m.statuses[tenantID.String()] = &ProvisioningStatus{
			TenantID:     tenantID,
			State:        StateFailed,
			Services:     m.createServiceStatuses(tenantID, ServiceStateFailed),
			ErrorMessage: err.Error(),
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		return err
	}

	// Check if already provisioned
	if status, exists := m.statuses[tenantID.String()]; exists && status.State == StateActive {
		// Idempotent: already provisioned, no-op
		return nil
	}

	// Simulate successful provisioning
	m.statuses[tenantID.String()] = &ProvisioningStatus{
		TenantID:  tenantID,
		State:     StateActive,
		Services:  m.createServiceStatuses(tenantID, ServiceStateMigrated),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return nil
}

// DeprovisionSchemas simulates schema deprovisioning for the tenant.
func (m *MockProvisioner) DeprovisionSchemas(_ context.Context, tenantID organization.OrganizationID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the call
	m.DeprovisioningCalls = append(m.DeprovisioningCalls, tenantID)

	// Check for simulated failure
	if err, shouldFail := m.FailDeprovisioningFor[tenantID.String()]; shouldFail {
		return err
	}

	// Remove the status (idempotent: no error if not found)
	delete(m.statuses, tenantID.String())

	return nil
}

// GetProvisioningStatus retrieves the current provisioning state for a tenant.
func (m *MockProvisioner) GetProvisioningStatus(_ context.Context, tenantID organization.OrganizationID) (*ProvisioningStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status, exists := m.statuses[tenantID.String()]
	if !exists {
		return nil, ErrProvisioningStatusNotFound
	}

	// Return a copy to prevent external mutation
	copyStatus := *status
	copyStatus.Services = make([]ServiceSchemaStatus, len(status.Services))
	copy(copyStatus.Services, status.Services)

	return &copyStatus, nil
}

// createServiceStatuses creates service status entries for all configured services.
func (m *MockProvisioner) createServiceStatuses(tenantID organization.OrganizationID, state ServiceProvisioningState) []ServiceSchemaStatus {
	statuses := make([]ServiceSchemaStatus, len(m.services))
	schemaName := tenantID.SchemaName()

	for i, svc := range m.services {
		statuses[i] = ServiceSchemaStatus{
			ServiceName:      svc.Name,
			SchemaName:       schemaName,
			State:            state,
			MigrationVersion: "mock-v1",
		}
	}

	return statuses
}

// Reset clears all state for testing.
func (m *MockProvisioner) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.statuses = make(map[string]*ProvisioningStatus)
	m.ProvisioningCalls = make([]organization.OrganizationID, 0)
	m.DeprovisioningCalls = make([]organization.OrganizationID, 0)
	m.FailProvisioningFor = make(map[string]error)
	m.FailDeprovisioningFor = make(map[string]error)
	m.ProvisioningDelay = 0
}

// SetStatus allows tests to manually set the provisioning status for a tenant.
func (m *MockProvisioner) SetStatus(status *ProvisioningStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.statuses[status.TenantID.String()] = status
}

// Ensure MockProvisioner implements SchemaProvisioner.
var _ SchemaProvisioner = (*MockProvisioner)(nil)
