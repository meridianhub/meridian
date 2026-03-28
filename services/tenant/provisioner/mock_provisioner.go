package provisioner

import (
	"context"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
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

	// FailPurgeFor lists tenant IDs that should fail during purging
	FailPurgeFor map[string]error

	// ProvisioningCalls tracks calls to ProvisionSchemas for verification
	ProvisioningCalls []tenant.TenantID

	// DeprovisioningCalls tracks calls to DeprovisionSchemas for verification
	DeprovisioningCalls []tenant.TenantID

	// PurgeCalls tracks calls to PurgeSchemas for verification
	PurgeCalls []tenant.TenantID

	// DataRetentionPeriod for testing retention enforcement
	DataRetentionPeriod time.Duration

	// OnProvisionAttempt is called after each provisioning attempt (before returning).
	// Useful for deterministic testing of retry behavior without polling.
	// The callback receives the tenant ID and the current attempt count for that tenant.
	// Set to nil to disable (default).
	OnProvisionAttempt func(tenantID string, attemptCount int)
}

// NewMockProvisioner creates a new mock provisioner with the given service configuration.
func NewMockProvisioner(services []ServiceConfig) *MockProvisioner {
	return &MockProvisioner{
		statuses:              make(map[string]*ProvisioningStatus),
		services:              services,
		FailProvisioningFor:   make(map[string]error),
		FailDeprovisioningFor: make(map[string]error),
		FailPurgeFor:          make(map[string]error),
		ProvisioningCalls:     make([]tenant.TenantID, 0),
		DeprovisioningCalls:   make([]tenant.TenantID, 0),
		PurgeCalls:            make([]tenant.TenantID, 0),
		DataRetentionPeriod:   0, // No retention period by default for testing
	}
}

// ProvisionSchemas simulates schema provisioning for the tenant.
//
// Note on locking: The lock is intentionally released before invoking OnProvisionAttempt
// callback and re-acquired afterward. This allows test code to modify MockProvisioner state
// (e.g., clearing FailProvisioningFor) during the callback. As a result, there's a window
// where concurrent goroutines could modify ProvisioningCalls between unlock and re-lock.
// This is acceptable for testing purposes where the callback mechanism provides deterministic
// control over the test flow.
func (m *MockProvisioner) ProvisionSchemas(ctx context.Context, tenantID tenant.TenantID) error {
	attemptCount, callback := m.recordProvisioningCall(tenantID)

	// Invoke callback outside lock to allow test code to modify state (e.g., ClearFailure)
	if callback != nil {
		callback(tenantID.String(), attemptCount)
	}

	// Re-acquire lock for the rest of the operation
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for concurrent provisioning attempt
	if status, exists := m.statuses[tenantID.String()]; exists && status.State == StateInProgress {
		return ErrProvisioningInProgress
	}

	// Simulate delay if configured
	if m.ProvisioningDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.ProvisioningDelay):
		}
	}

	return m.simulateProvisioning(tenantID)
}

// recordProvisioningCall records the call and calculates attempt count.
// Returns the attempt count and captured callback. Releases the lock before returning.
func (m *MockProvisioner) recordProvisioningCall(tenantID tenant.TenantID) (int, func(string, int)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ProvisioningCalls = append(m.ProvisioningCalls, tenantID)

	attemptCount := 0
	for _, calledID := range m.ProvisioningCalls {
		if calledID.String() == tenantID.String() {
			attemptCount++
		}
	}

	return attemptCount, m.OnProvisionAttempt
}

// simulateProvisioning handles the mock provisioning result (failure, idempotent, or success).
// Must be called with m.mu held.
func (m *MockProvisioner) simulateProvisioning(tenantID tenant.TenantID) error {
	// Check for simulated failure
	if err, shouldFail := m.FailProvisioningFor[tenantID.String()]; shouldFail {
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

	// Idempotent: already provisioned, no-op
	if status, exists := m.statuses[tenantID.String()]; exists && status.State == StateActive {
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

// DeprovisionSchemas simulates schema deprovisioning (soft delete) for the tenant.
func (m *MockProvisioner) DeprovisionSchemas(_ context.Context, tenantID tenant.TenantID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the call
	m.DeprovisioningCalls = append(m.DeprovisioningCalls, tenantID)

	// Check for simulated failure
	if err, shouldFail := m.FailDeprovisioningFor[tenantID.String()]; shouldFail {
		return err
	}

	// Get existing status
	status, exists := m.statuses[tenantID.String()]
	if !exists {
		return ErrProvisioningStatusNotFound
	}

	// Idempotent: already deprovisioned
	if status.State == StateDeprovisioned {
		return nil
	}

	// Soft delete: mark as deprovisioned
	now := time.Now()
	status.State = StateDeprovisioned
	status.DeprovisionedAt = &now
	status.UpdatedAt = now

	return nil
}

// PurgeSchemas simulates permanent schema deletion after retention period.
func (m *MockProvisioner) PurgeSchemas(_ context.Context, tenantID tenant.TenantID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the call
	m.PurgeCalls = append(m.PurgeCalls, tenantID)

	// Check for simulated failure
	if err, shouldFail := m.FailPurgeFor[tenantID.String()]; shouldFail {
		return err
	}

	// Get existing status
	status, exists := m.statuses[tenantID.String()]
	if !exists {
		return ErrProvisioningStatusNotFound
	}

	// Must be deprovisioned first
	if status.State != StateDeprovisioned {
		return ErrNotDeprovisioned
	}

	// Check retention period
	if status.DeprovisionedAt != nil && m.DataRetentionPeriod > 0 {
		retentionEnd := status.DeprovisionedAt.Add(m.DataRetentionPeriod)
		if time.Now().Before(retentionEnd) {
			return ErrRetentionPeriodNotElapsed
		}
	}

	// Remove the status record - matches interface contract:
	// "Removes the provisioning status record (purge completes the lifecycle)"
	delete(m.statuses, tenantID.String())

	return nil
}

// GetProvisioningStatus retrieves the current provisioning state for a tenant.
func (m *MockProvisioner) GetProvisioningStatus(_ context.Context, tenantID tenant.TenantID) (*ProvisioningStatus, error) {
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
func (m *MockProvisioner) createServiceStatuses(tenantID tenant.TenantID, state ServiceProvisioningState) []ServiceSchemaStatus {
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
	m.ProvisioningCalls = make([]tenant.TenantID, 0)
	m.DeprovisioningCalls = make([]tenant.TenantID, 0)
	m.PurgeCalls = make([]tenant.TenantID, 0)
	m.FailProvisioningFor = make(map[string]error)
	m.FailDeprovisioningFor = make(map[string]error)
	m.FailPurgeFor = make(map[string]error)
	m.ProvisioningDelay = 0
	m.DataRetentionPeriod = 0
}

// SetStatus allows tests to manually set the provisioning status for a tenant.
func (m *MockProvisioner) SetStatus(status *ProvisioningStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.statuses[status.TenantID.String()] = status
}

// ReconcileMigrations simulates reconciling migrations for existing tenants.
// In the mock, this is a no-op that returns success. Tests can verify calls
// via ReconciliationCalls if needed.
func (m *MockProvisioner) ReconcileMigrations(_ context.Context, tenantID *tenant.TenantID) (int, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tenantID != nil {
		// Single tenant reconciliation
		if status, exists := m.statuses[tenantID.String()]; exists && status.State == StateActive {
			return 1, nil
		}
		return 0, nil
	}

	// All tenants reconciliation - count active tenants
	count := 0
	for _, status := range m.statuses {
		if status.State == StateActive {
			count++
		}
	}
	return count, nil
}

// GetRequiredSchemas returns the list of service names that require schema provisioning.
func (m *MockProvisioner) GetRequiredSchemas() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	schemas := make([]string, 0, len(m.services))
	for _, svc := range m.services {
		schemas = append(schemas, svc.Name)
	}
	return schemas
}

// InitializeProvisioningStatus creates an initial provisioning_status record with 'pending' state.
func (m *MockProvisioner) InitializeProvisioningStatus(_ context.Context, tenantID tenant.TenantID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Idempotency: if status already exists, no-op
	if _, exists := m.statuses[tenantID.String()]; exists {
		return nil
	}

	// Create initial status with pending state
	m.statuses[tenantID.String()] = &ProvisioningStatus{
		TenantID:  tenantID,
		State:     StatePending,
		Services:  m.createServiceStatuses(tenantID, ServiceStatePending),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return nil
}

// GetProvisioningCallCount returns the number of times ProvisionSchemas was called.
// Thread-safe for concurrent access during tests.
func (m *MockProvisioner) GetProvisioningCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.ProvisioningCalls)
}

// GetProvisioningCallCountForTenant returns the number of times ProvisionSchemas was called
// for a specific tenant. Thread-safe for concurrent access during tests.
func (m *MockProvisioner) GetProvisioningCallCountForTenant(tenantID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, calledID := range m.ProvisioningCalls {
		if calledID.String() == tenantID {
			count++
		}
	}
	return count
}

// ClearFailure removes a tenant from the FailProvisioningFor map.
// Thread-safe for concurrent access during tests.
// Returns true if the tenant was in the map and removed, false otherwise.
func (m *MockProvisioner) ClearFailure(tenantID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.FailProvisioningFor[tenantID]; exists {
		delete(m.FailProvisioningFor, tenantID)
		return true
	}
	return false
}

// Ensure MockProvisioner implements SchemaProvisioner.
var _ SchemaProvisioner = (*MockProvisioner)(nil)
