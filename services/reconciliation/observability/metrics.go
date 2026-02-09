package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// BalanceImbalanceGauge tracks the current imbalance amount per instrument code.
	// In a healthy system, this gauge should always be 0.
	// A non-zero value indicates a P1/Critical ledger integrity violation.
	BalanceImbalanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "balance_imbalance_amount",
			Help:      "Current imbalance amount per instrument code. Should always be 0 in a healthy system.",
		},
		[]string{"instrument_code"},
	)

	// BalanceAssertionTotal counts the total number of balance assertions by status.
	BalanceAssertionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "balance_assertion_total",
			Help:      "Total number of balance assertions executed, labeled by result status.",
		},
		[]string{"status", "scope"},
	)

	// PersistentImbalanceGauge tracks the number of consecutive days of persistent imbalance.
	PersistentImbalanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "reconciliation",
			Name:      "persistent_imbalance_days",
			Help:      "Number of consecutive days of imbalance per instrument code.",
		},
		[]string{"instrument_code"},
	)
)
