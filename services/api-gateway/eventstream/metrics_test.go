package eventstream_test

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestMetrics creates a Metrics instance backed by a fresh registry so tests
// do not interfere with each other or the global default registry.
func newTestMetrics(t *testing.T) *eventstream.Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := eventstream.NewMetrics(reg)
	require.NoError(t, err)
	return m
}

// --- Registration ---

func TestNewMetrics_RegistersAllMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := eventstream.NewMetrics(reg)
	require.NoError(t, err)

	// Prime all five collectors so they appear in Gather output.
	m.IncConnectionOpened("t")
	m.IncEventDelivered("t", "ch")
	m.IncEventDropped("reason")
	m.SetSubscriptionCount(1)
	m.ObserveLatency(time.Millisecond)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	names := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}

	required := []string{
		"meridian_ws_connections_active",
		"meridian_ws_events_delivered_total",
		"meridian_ws_events_dropped_total",
		"meridian_ws_subscription_count",
		"meridian_ws_event_latency_seconds",
	}
	for _, name := range required {
		assert.True(t, names[name], "expected metric %q to be registered", name)
	}
}

func TestNewMetrics_NilRegisterer_ReturnsError(t *testing.T) {
	_, err := eventstream.NewMetrics(nil)
	require.ErrorIs(t, err, eventstream.ErrNilRegisterer)
}

func TestNewMetrics_DuplicateRegistration_ReturnsError(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, err := eventstream.NewMetrics(reg)
	require.NoError(t, err)

	// A second call with the same registry must fail because the metric names
	// are already registered.
	_, err = eventstream.NewMetrics(reg)
	require.Error(t, err)
}

// --- IncConnectionOpened / IncConnectionClosed ---

func TestMetrics_ConnectionOpened_SingleTenant_IncrementsGauge(t *testing.T) {
	m := newTestMetrics(t)

	m.IncConnectionOpened("tenant-A")
	m.IncConnectionOpened("tenant-A")

	// Single label combination; testutil.ToFloat64 is safe.
	value := testutil.ToFloat64(m.ActiveConnectionsForTenant("tenant-A"))
	assert.InDelta(t, 2.0, value, 0.001)
}

func TestMetrics_ConnectionOpened_MultiTenant_TracksSeparately(t *testing.T) {
	m := newTestMetrics(t)

	m.IncConnectionOpened("tenant-A")
	m.IncConnectionOpened("tenant-B")

	// Two distinct label combinations exist in the gauge vec.
	count := testutil.CollectAndCount(m.ActiveConnections())
	assert.Equal(t, 2, count)
}

func TestMetrics_ConnectionClosedDecrementsGauge(t *testing.T) {
	m := newTestMetrics(t)

	m.IncConnectionOpened("tenant-A")
	m.IncConnectionOpened("tenant-A")
	m.IncConnectionClosed("tenant-A", "idle_timeout")

	value := testutil.ToFloat64(m.ActiveConnectionsForTenant("tenant-A"))
	assert.InDelta(t, 1.0, value, 0.001)
}

// --- IncEventDelivered ---

func TestMetrics_IncEventDelivered_IncrementsByLabelValues(t *testing.T) {
	m := newTestMetrics(t)

	m.IncEventDelivered("tenant-X", "payment-order.*")
	m.IncEventDelivered("tenant-X", "payment-order.*")
	m.IncEventDelivered("tenant-Y", "audit.*")

	// Two label combinations should yield two counter families.
	count := testutil.CollectAndCount(m.EventsDelivered())
	assert.Equal(t, 2, count)
}

// --- IncEventDropped ---

func TestMetrics_IncEventDropped_IncrementsByReason(t *testing.T) {
	m := newTestMetrics(t)

	m.IncEventDropped("buffer_full")
	m.IncEventDropped("buffer_full")
	m.IncEventDropped("no_subscriber")

	count := testutil.CollectAndCount(m.EventsDropped())
	assert.Equal(t, 2, count)
}

// --- ObserveLatency ---

func TestMetrics_ObserveLatency_RecordsHistogramSample(t *testing.T) {
	m := newTestMetrics(t)

	m.ObserveLatency(10 * time.Millisecond)
	m.ObserveLatency(500 * time.Millisecond)
	m.ObserveLatency(2 * time.Second)

	// Histogram collector returns exactly one metric family sample.
	count := testutil.CollectAndCount(m.EventLatency())
	assert.GreaterOrEqual(t, count, 1)
}

// --- SetSubscriptionCount ---

func TestMetrics_SetSubscriptionCount_SetsGaugeValue(t *testing.T) {
	m := newTestMetrics(t)

	m.SetSubscriptionCount(42)

	value := testutil.ToFloat64(m.SubscriptionCount())
	assert.InDelta(t, 42.0, value, 0.001)
}

func TestMetrics_SetSubscriptionCount_IsOverwritten(t *testing.T) {
	m := newTestMetrics(t)

	m.SetSubscriptionCount(10)
	m.SetSubscriptionCount(5)

	value := testutil.ToFloat64(m.SubscriptionCount())
	assert.InDelta(t, 5.0, value, 0.001)
}
