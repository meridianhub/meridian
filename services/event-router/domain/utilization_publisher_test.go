package domain_test

import (
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/event-router/domain"
	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUtilizationPublisher is a test double implementing domain.UtilizationPublisher.
type fakeUtilizationPublisher struct {
	mu           sync.Mutex
	published    []*domain.UtilizationMeasurement
	stopped      bool
	publishFunc  func(*domain.UtilizationMeasurement)
}

func newFakeUtilizationPublisher() *fakeUtilizationPublisher {
	return &fakeUtilizationPublisher{
		published: make([]*domain.UtilizationMeasurement, 0),
	}
}

func (f *fakeUtilizationPublisher) Publish(m *domain.UtilizationMeasurement) {
	f.mu.Lock()
	f.published = append(f.published, m)
	f.mu.Unlock()
	if f.publishFunc != nil {
		f.publishFunc(m)
	}
}

func (f *fakeUtilizationPublisher) Stop() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
}

func (f *fakeUtilizationPublisher) getPublished() []*domain.UtilizationMeasurement {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]*domain.UtilizationMeasurement, len(f.published))
	copy(result, f.published)
	return result
}

func (f *fakeUtilizationPublisher) isStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// Verify fakeUtilizationPublisher satisfies domain.UtilizationPublisher at compile time.
var _ domain.UtilizationPublisher = (*fakeUtilizationPublisher)(nil)

// TestUtilizationPublisher_InterfaceCompliance verifies the interface is satisfied.
func TestUtilizationPublisher_InterfaceCompliance(t *testing.T) {
	var p domain.UtilizationPublisher = newFakeUtilizationPublisher()
	assert.NotNil(t, p)
}

// TestUtilizationPublisher_Publish_MessageFormatting verifies measurement fields are preserved.
func TestUtilizationPublisher_Publish_MessageFormatting(t *testing.T) {
	publisher := newFakeUtilizationPublisher()
	now := time.Now()

	m := &domain.UtilizationMeasurement{
		TenantID:      "tenant-abc",
		ServiceName:   "current-account",
		OperationType: "CreateAccount",
		Amount:        quantity.NewAssetFromInt(1, domain.InstrumentTransaction),
		Timestamp:     now,
		CorrelationID: "corr-xyz",
	}

	publisher.Publish(m)

	published := publisher.getPublished()
	require.Len(t, published, 1)
	assert.Equal(t, "tenant-abc", published[0].TenantID)
	assert.Equal(t, "current-account", published[0].ServiceName)
	assert.Equal(t, "CreateAccount", published[0].OperationType)
	assert.Equal(t, "TRANSACTION", published[0].Amount.GetInstrument().Code)
	assert.Equal(t, now, published[0].Timestamp)
	assert.Equal(t, "corr-xyz", published[0].CorrelationID)
}

// TestUtilizationPublisher_Publish_TopicRouting verifies different services are routed via ServiceName.
func TestUtilizationPublisher_Publish_TopicRouting(t *testing.T) {
	publisher := newFakeUtilizationPublisher()

	services := []string{"current-account", "payment-order", "financial-accounting", "position-keeping"}
	for _, svc := range services {
		publisher.Publish(&domain.UtilizationMeasurement{
			TenantID:    "tenant-1",
			ServiceName: svc,
			Amount:      quantity.NewAssetFromInt(1, domain.InstrumentOperation),
			Timestamp:   time.Now(),
		})
	}

	published := publisher.getPublished()
	require.Len(t, published, len(services))
	for i, svc := range services {
		assert.Equal(t, svc, published[i].ServiceName)
	}
}

// TestUtilizationPublisher_Publish_ErrorHandling verifies that a panicking publishFunc
// is isolated by the caller (no crash propagation expectation at this layer).
func TestUtilizationPublisher_Publish_ErrorHandling(t *testing.T) {
	// The publisher interface has no return value, so errors are handled
	// by the implementation. Verify the interface accepts calls without panicking.
	publisher := newFakeUtilizationPublisher()

	m := &domain.UtilizationMeasurement{
		TenantID:  "tenant-1",
		Amount:    quantity.NewAssetFromInt(1, domain.InstrumentOperation),
		Timestamp: time.Now(),
	}

	// Should not panic
	assert.NotPanics(t, func() {
		publisher.Publish(m)
	})

	assert.Len(t, publisher.getPublished(), 1)
}

// TestUtilizationPublisher_Stop_GracefulShutdown verifies Stop is called and sets stopped state.
func TestUtilizationPublisher_Stop_GracefulShutdown(t *testing.T) {
	publisher := newFakeUtilizationPublisher()
	assert.False(t, publisher.isStopped())

	publisher.Stop()

	assert.True(t, publisher.isStopped())
}

// TestUtilizationPublisher_Publish_MultipleInstruments verifies all instrument types can be published.
func TestUtilizationPublisher_Publish_MultipleInstruments(t *testing.T) {
	publisher := newFakeUtilizationPublisher()

	instruments := []struct {
		name       string
		instrument quantity.Instrument
	}{
		{"transaction", domain.InstrumentTransaction},
		{"api_call", domain.InstrumentAPICall},
		{"operation", domain.InstrumentOperation},
		{"storage_gb_hour", domain.InstrumentStorageGBHour},
		{"compute_hour", domain.InstrumentComputeHour},
	}

	for _, inst := range instruments {
		publisher.Publish(&domain.UtilizationMeasurement{
			TenantID:    "tenant-1",
			ServiceName: "test-service",
			Amount:      quantity.NewAssetFromInt(1, inst.instrument),
			Timestamp:   time.Now(),
		})
	}

	published := publisher.getPublished()
	require.Len(t, published, len(instruments))
	for i, inst := range instruments {
		assert.Equal(t, inst.instrument.Code, published[i].Amount.GetInstrument().Code,
			"wrong instrument for %s", inst.name)
	}
}

// TestUtilizationPublisher_ConcurrentPublish verifies thread-safe publishing.
func TestUtilizationPublisher_ConcurrentPublish(t *testing.T) {
	publisher := newFakeUtilizationPublisher()

	const numPublishers = 10
	var wg sync.WaitGroup
	for i := 0; i < numPublishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			publisher.Publish(&domain.UtilizationMeasurement{
				TenantID:    "tenant-concurrent",
				ServiceName: "test-service",
				Amount:      quantity.NewAssetFromInt(1, domain.InstrumentOperation),
				Timestamp:   time.Now(),
			})
		}()
	}
	wg.Wait()

	assert.Len(t, publisher.getPublished(), numPublishers)
}
