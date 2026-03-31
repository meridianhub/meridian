package email

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics provides Prometheus metrics for the email outbox dispatch pipeline.
type Metrics struct {
	pendingTotal        prometheus.Gauge
	sendDuration        prometheus.Histogram
	sendErrorsTotal     *prometheus.CounterVec
	deadLetterTotal     prometheus.Counter
	cancelledTotal      prometheus.Counter
	circuitBreakerSt    prometheus.Gauge
	emailsSentTotal     *prometheus.CounterVec
	emailComplaintsTotal *prometheus.CounterVec
}

// NewMetrics creates email metrics auto-registered with the default registry.
func NewMetrics() *Metrics {
	return newMetrics(promauto.With(prometheus.DefaultRegisterer))
}

// NewMetricsWithRegistry creates email metrics registered with a custom registry.
func NewMetricsWithRegistry(reg prometheus.Registerer) *Metrics {
	return newMetrics(promauto.With(reg))
}

func newMetrics(factory promauto.Factory) *Metrics {
	return &Metrics{
		pendingTotal: factory.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "meridian",
				Subsystem: "email_outbox",
				Name:      "pending_total",
				Help:      "Current number of pending email outbox rows.",
			},
		),
		sendDuration: factory.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "meridian",
				Subsystem: "email_outbox",
				Name:      "send_duration_seconds",
				Help:      "Duration of email send API calls in seconds.",
				Buckets:   []float64{.05, .1, .25, .5, 1, 2.5, 5, 10},
			},
		),
		sendErrorsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "email_outbox",
				Name:      "send_errors_total",
				Help:      "Total number of failed email sends by template and error type.",
			},
			[]string{"template", "error_type"},
		),
		deadLetterTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "email_outbox",
				Name:      "dead_letter_total",
				Help:      "Total number of emails that exhausted all retry attempts.",
			},
		),
		cancelledTotal: factory.NewCounter(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Subsystem: "email_outbox",
				Name:      "cancelled_total",
				Help:      "Total number of cancelled emails (e.g., dunning for paid invoices).",
			},
		),
		circuitBreakerSt: factory.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "meridian",
				Subsystem: "email_outbox",
				Name:      "circuit_breaker_state",
				Help:      "Current circuit breaker state: 0=closed, 1=half-open, 2=open.",
			},
		),
		emailsSentTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Name:      "email_sent_total",
				Help:      "Total emails sent successfully per tenant.",
			},
			[]string{"tenant_id"},
		),
		emailComplaintsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "meridian",
				Name:      "email_complaints_total",
				Help:      "Total email complaints received per tenant.",
			},
			[]string{"tenant_id"},
		),
	}
}

// SetPendingTotal sets the current pending outbox gauge.
func (m *Metrics) SetPendingTotal(n float64) { m.pendingTotal.Set(n) }

// ObserveSendDuration records the duration of an email send.
func (m *Metrics) ObserveSendDuration(seconds float64) { m.sendDuration.Observe(seconds) }

// RecordSendError increments the send error counter.
func (m *Metrics) RecordSendError(template, errorType string) {
	m.sendErrorsTotal.WithLabelValues(template, errorType).Inc()
}

// RecordDeadLetter increments the dead letter counter.
func (m *Metrics) RecordDeadLetter() { m.deadLetterTotal.Inc() }

// RecordCancelled increments the cancelled counter.
func (m *Metrics) RecordCancelled() { m.cancelledTotal.Inc() }

// SetCircuitBreakerState sets the circuit breaker state gauge.
// 0=closed, 1=half-open, 2=open.
func (m *Metrics) SetCircuitBreakerState(state float64) { m.circuitBreakerSt.Set(state) }

// RecordEmailSent increments the per-tenant sent counter.
func (m *Metrics) RecordEmailSent(tenantID string) {
	m.emailsSentTotal.WithLabelValues(tenantID).Inc()
}

// RecordEmailComplaint increments the per-tenant complaint counter.
// Alert fires when rate(complaints[7d]) / rate(sent[7d]) > 0.001 (0.1%).
func (m *Metrics) RecordEmailComplaint(tenantID string) {
	m.emailComplaintsTotal.WithLabelValues(tenantID).Inc()
}
