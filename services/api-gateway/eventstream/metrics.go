package eventstream

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Latency histogram buckets: 10ms to 2.5s, suitable for WebSocket event delivery.
var latencyBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5}

// Metrics holds Prometheus metric collectors for the WebSocket event-streaming layer.
// Create a new instance per service via NewMetrics and wire it into the Router,
// Handler, and Connection components.
//
// The recording methods (IncConnectionOpened, IncConnectionClosed, IncEventDelivered,
// IncEventDropped, ObserveLatency, SetSubscriptionCount) are nil-safe and may be called
// on a nil *Metrics as a no-op. Accessor methods (ActiveConnections, EventsDelivered,
// etc.) are intended for use in tests only and are not nil-safe.
type Metrics struct {
	activeConnections *prometheus.GaugeVec
	eventsDelivered   *prometheus.CounterVec
	eventsDropped     *prometheus.CounterVec
	subscriptionCount prometheus.Gauge
	eventLatency      prometheus.Histogram
}

// ErrNilRegisterer is returned by NewMetrics when reg is nil.
var ErrNilRegisterer = errors.New("eventstream: prometheus registerer must not be nil")

// NewMetrics registers all eventstream Prometheus metrics with the given registry
// and returns a Metrics wrapper. An error is returned if reg is nil or if any
// metric name is already registered in the provided registry.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		return nil, ErrNilRegisterer
	}

	m := buildMetricCollectors()

	if err := registerCollectors(reg, m); err != nil {
		return nil, err
	}

	return m, nil
}

// buildMetricCollectors creates all eventstream Prometheus metric collectors.
func buildMetricCollectors() *Metrics {
	return &Metrics{
		activeConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "meridian_ws_connections_active",
				Help: "Number of active WebSocket connections.",
			},
			[]string{"tenant_id"},
		),
		eventsDelivered: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "meridian_ws_events_delivered_total",
				Help: "Total number of events successfully delivered to WebSocket clients.",
			},
			[]string{"tenant_id", "channel"},
		),
		eventsDropped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "meridian_ws_events_dropped_total",
				Help: "Total number of events dropped before delivery.",
			},
			[]string{"reason"},
		),
		subscriptionCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "meridian_ws_subscription_count",
				Help: "Current number of active WebSocket subscriptions.",
			},
		),
		eventLatency: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "meridian_ws_event_latency_seconds",
				Help:    "Publish-to-delivery latency for WebSocket events in seconds.",
				Buckets: latencyBuckets,
			},
		),
	}
}

// registerCollectors registers all metric collectors with the given registry.
// On partial failure, already-registered collectors are unregistered.
func registerCollectors(reg prometheus.Registerer, m *Metrics) error {
	collectors := []prometheus.Collector{
		m.activeConnections,
		m.eventsDelivered,
		m.eventsDropped,
		m.subscriptionCount,
		m.eventLatency,
	}
	var registered []prometheus.Collector
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			for _, r := range registered {
				reg.Unregister(r)
			}
			return err
		}
		registered = append(registered, c)
	}
	return nil
}

// IncConnectionOpened increments the active connection gauge for the given tenant.
func (m *Metrics) IncConnectionOpened(tenantID string) {
	if m == nil {
		return
	}
	m.activeConnections.WithLabelValues(tenantID).Inc()
}

// IncConnectionClosed decrements the active connection gauge for the given tenant.
// reason is recorded in logs but not as a metric label to avoid high cardinality.
func (m *Metrics) IncConnectionClosed(tenantID, _ string) {
	if m == nil {
		return
	}
	m.activeConnections.WithLabelValues(tenantID).Dec()
}

// IncEventDelivered increments the delivered-events counter for the given tenant and channel.
func (m *Metrics) IncEventDelivered(tenantID, channel string) {
	if m == nil {
		return
	}
	m.eventsDelivered.WithLabelValues(tenantID, channel).Inc()
}

// IncEventDropped increments the dropped-events counter for the given reason.
// Recognized reasons: "buffer_full", "no_subscriber".
func (m *Metrics) IncEventDropped(reason string) {
	if m == nil {
		return
	}
	m.eventsDropped.WithLabelValues(reason).Inc()
}

// ObserveLatency records an event's publish-to-delivery latency.
func (m *Metrics) ObserveLatency(d time.Duration) {
	if m == nil {
		return
	}
	m.eventLatency.Observe(d.Seconds())
}

// SetSubscriptionCount sets the current total number of active subscriptions.
func (m *Metrics) SetSubscriptionCount(n int) {
	if m == nil {
		return
	}
	m.subscriptionCount.Set(float64(n))
}

// ActiveConnections exposes the underlying gauge vec for use in tests.
func (m *Metrics) ActiveConnections() prometheus.Collector { return m.activeConnections }

// ActiveConnectionsForTenant returns the gauge for a specific tenant label,
// allowing testutil.ToFloat64 to work with a single-sample collector.
func (m *Metrics) ActiveConnectionsForTenant(tenantID string) prometheus.Gauge {
	return m.activeConnections.WithLabelValues(tenantID)
}

// EventsDelivered exposes the underlying counter vec for use in tests.
func (m *Metrics) EventsDelivered() prometheus.Collector { return m.eventsDelivered }

// EventsDropped exposes the underlying counter vec for use in tests.
func (m *Metrics) EventsDropped() prometheus.Collector { return m.eventsDropped }

// SubscriptionCount exposes the underlying gauge for use in tests.
func (m *Metrics) SubscriptionCount() prometheus.Collector { return m.subscriptionCount }

// EventLatency exposes the underlying histogram for use in tests.
func (m *Metrics) EventLatency() prometheus.Collector { return m.eventLatency }
