// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
)

// AlertRecord represents a single alert that was sent or attempted.
// Used by mock implementations to capture alerts for testing.
type AlertRecord struct {
	// AlertType identifies the provider ("pagerduty" or "slack").
	AlertType string

	// TenantID is the ID of the tenant the alert relates to.
	TenantID string

	// Payload contains the alert data.
	Payload AlertPayload

	// Timestamp is when the alert was sent.
	Timestamp time.Time

	// Success indicates whether the alert was successfully sent.
	Success bool

	// Error contains any error that occurred.
	Error error
}

// InMemoryAlertStore is a thread-safe store for capturing alerts during testing.
// It allows tests to verify which alerts were sent without making real API calls.
type InMemoryAlertStore struct {
	mu      sync.RWMutex
	records []AlertRecord
}

// NewInMemoryAlertStore creates a new in-memory alert store.
func NewInMemoryAlertStore() *InMemoryAlertStore {
	return &InMemoryAlertStore{
		records: make([]AlertRecord, 0),
	}
}

// Record stores an alert record.
func (s *InMemoryAlertStore) Record(record AlertRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
}

// Records returns all recorded alerts.
// Returns a copy to prevent external modification.
func (s *InMemoryAlertStore) Records() []AlertRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]AlertRecord, len(s.records))
	copy(result, s.records)
	return result
}

// Count returns the number of recorded alerts.
func (s *InMemoryAlertStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// CountByType returns the number of alerts for a specific type.
func (s *InMemoryAlertStore) CountByType(alertType string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, r := range s.records {
		if r.AlertType == alertType {
			count++
		}
	}
	return count
}

// CountSuccessful returns the number of successfully sent alerts.
func (s *InMemoryAlertStore) CountSuccessful() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, r := range s.records {
		if r.Success {
			count++
		}
	}
	return count
}

// Clear removes all recorded alerts.
func (s *InMemoryAlertStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = make([]AlertRecord, 0)
}

// FindByTenantID returns all alerts for a specific tenant.
func (s *InMemoryAlertStore) FindByTenantID(tenantID string) []AlertRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []AlertRecord
	for _, r := range s.records {
		if r.TenantID == tenantID {
			result = append(result, r)
		}
	}
	return result
}

// AlertSender is an interface for sending alerts.
// Implemented by both real clients (PagerDutyClient, SlackNotifier) and mocks.
type AlertSender interface {
	// SendAlert sends an alert for the given tenant.
	// Returns an error if the alert could not be sent.
	SendAlert(ctx context.Context, tenant *domain.Tenant) error

	// IsEnabled returns true if the sender is enabled.
	IsEnabled() bool
}

// MockPagerDutyClient is a mock implementation of PagerDuty client for testing.
type MockPagerDutyClient struct {
	mu       sync.Mutex
	store    *InMemoryAlertStore
	enabled  bool
	failNext bool                 // If true, the next call will fail
	failErr  error                // Error to return when failNext is true
	calls    int                  // Track number of calls
	callback func(*domain.Tenant) // Optional callback on each call
}

// MockPagerDutyOption configures the MockPagerDutyClient.
type MockPagerDutyOption func(*MockPagerDutyClient)

// WithMockPagerDutyStore sets the alert store for the mock.
func WithMockPagerDutyStore(store *InMemoryAlertStore) MockPagerDutyOption {
	return func(m *MockPagerDutyClient) {
		m.store = store
	}
}

// WithMockPagerDutyCallback sets a callback to be invoked on each call.
func WithMockPagerDutyCallback(fn func(*domain.Tenant)) MockPagerDutyOption {
	return func(m *MockPagerDutyClient) {
		m.callback = fn
	}
}

// NewMockPagerDutyClient creates a new mock PagerDuty client.
func NewMockPagerDutyClient(enabled bool, opts ...MockPagerDutyOption) *MockPagerDutyClient {
	m := &MockPagerDutyClient{
		enabled: enabled,
		store:   NewInMemoryAlertStore(),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// TriggerAlert mocks triggering a PagerDuty alert.
func (m *MockPagerDutyClient) TriggerAlert(_ context.Context, summary, dedupKey string, severity interface{}, customDetails map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++

	// Check if we should fail
	if m.failNext {
		m.failNext = false
		return m.failErr
	}

	// Extract tenant ID from custom details
	tenantID := ""
	if id, ok := customDetails["tenant_id"].(string); ok {
		tenantID = id
	}

	// Record the alert
	record := AlertRecord{
		AlertType: AlertTypePagerDuty,
		TenantID:  tenantID,
		Payload: AlertPayload{
			Summary:       summary,
			DedupKey:      dedupKey,
			Severity:      severityToString(severity),
			CustomDetails: customDetails,
		},
		Timestamp: time.Now(),
		Success:   true,
	}
	m.store.Record(record)

	return nil
}

// severityToString converts a severity value to string.
func severityToString(severity interface{}) string {
	switch s := severity.(type) {
	case string:
		return s
	default:
		// Handle custom severity types (like clients.Severity) by converting to string
		return fmt.Sprintf("%v", s)
	}
}

// IsEnabled returns whether the mock client is enabled.
func (m *MockPagerDutyClient) IsEnabled() bool {
	return m.enabled
}

// SetFailNext configures the mock to fail the next call with the given error.
func (m *MockPagerDutyClient) SetFailNext(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = true
	m.failErr = err
}

// Calls returns the number of times TriggerAlert was called.
func (m *MockPagerDutyClient) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// Store returns the underlying alert store.
func (m *MockPagerDutyClient) Store() *InMemoryAlertStore {
	return m.store
}

// Reset clears the mock state.
func (m *MockPagerDutyClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = 0
	m.failNext = false
	m.failErr = nil
	m.store.Clear()
}

// MockSlackNotifier is a mock implementation of Slack notifier for testing.
type MockSlackNotifier struct {
	mu       sync.Mutex
	store    *InMemoryAlertStore
	enabled  bool
	failNext bool
	failErr  error
	calls    int
}

// MockSlackOption configures the MockSlackNotifier.
type MockSlackOption func(*MockSlackNotifier)

// WithMockSlackStore sets the alert store for the mock.
func WithMockSlackStore(store *InMemoryAlertStore) MockSlackOption {
	return func(m *MockSlackNotifier) {
		m.store = store
	}
}

// NewMockSlackNotifier creates a new mock Slack notifier.
func NewMockSlackNotifier(enabled bool, opts ...MockSlackOption) *MockSlackNotifier {
	m := &MockSlackNotifier{
		enabled: enabled,
		store:   NewInMemoryAlertStore(),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// NotifyProvisioningFailure mocks sending a Slack notification.
func (m *MockSlackNotifier) NotifyProvisioningFailure(_ context.Context, tenant *domain.Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++

	// Check if we should fail
	if m.failNext {
		m.failNext = false
		return m.failErr
	}

	// Record the alert
	record := AlertRecord{
		AlertType: AlertTypeSlack,
		TenantID:  tenant.ID.String(),
		Payload: AlertPayload{
			Summary:  "Tenant Provisioning Failed",
			Severity: "critical",
			CustomDetails: map[string]any{
				"tenant_id":     tenant.ID.String(),
				"display_name":  tenant.DisplayName,
				"error_message": tenant.ErrorMessage,
				"status":        string(tenant.Status),
			},
		},
		Timestamp: time.Now(),
		Success:   true,
	}
	m.store.Record(record)

	return nil
}

// SetFailNext configures the mock to fail the next call with the given error.
func (m *MockSlackNotifier) SetFailNext(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = true
	m.failErr = err
}

// Calls returns the number of times NotifyProvisioningFailure was called.
func (m *MockSlackNotifier) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// Store returns the underlying alert store.
func (m *MockSlackNotifier) Store() *InMemoryAlertStore {
	return m.store
}

// Reset clears the mock state.
func (m *MockSlackNotifier) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = 0
	m.failNext = false
	m.failErr = nil
	m.store.Clear()
}

// IsEnabled returns whether the mock notifier is enabled.
func (m *MockSlackNotifier) IsEnabled() bool {
	return m.enabled
}
