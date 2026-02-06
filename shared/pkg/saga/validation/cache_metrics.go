package validation

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// cacheHitsTotal tracks the total number of validation cache hits.
	cacheHitsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_validation_cache_hits_total",
			Help: "Total number of validation cache hits",
		},
	)

	// cacheMissesTotal tracks the total number of validation cache misses.
	cacheMissesTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "saga_validation_cache_misses_total",
			Help: "Total number of validation cache misses",
		},
	)

	// cacheSize tracks the current number of entries in the validation cache.
	cacheSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "saga_validation_cache_size",
			Help: "Current number of entries in validation cache",
		},
	)

	// cacheEvictionsTotal tracks the total cache evictions by reason.
	// Labels: reason ("ttl" for TTL expiration, "lru" for LRU eviction)
	cacheEvictionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "saga_validation_cache_evictions_total",
			Help: "Total cache evictions by reason",
		},
		[]string{"reason"},
	)
)

// RecordCacheHit records a validation cache hit.
func RecordCacheHit() {
	cacheHitsTotal.Inc()
}

// RecordCacheMiss records a validation cache miss.
func RecordCacheMiss() {
	cacheMissesTotal.Inc()
}

// RecordCacheSize updates the cache size gauge.
func RecordCacheSize(size int) {
	cacheSize.Set(float64(size))
}

// RecordCacheEviction records a cache eviction with the given reason.
// Valid reasons: "ttl" (TTL expiration), "lru" (LRU eviction)
func RecordCacheEviction(reason string) {
	cacheEvictionsTotal.WithLabelValues(reason).Inc()
}

// ExposeValidationCacheMetricsForTesting provides access to raw Prometheus metrics for testing.
var ExposeValidationCacheMetricsForTesting = struct {
	HitsTotal      prometheus.Counter
	MissesTotal    prometheus.Counter
	Size           prometheus.Gauge
	EvictionsTotal *prometheus.CounterVec
}{
	HitsTotal:      cacheHitsTotal,
	MissesTotal:    cacheMissesTotal,
	Size:           cacheSize,
	EvictionsTotal: cacheEvictionsTotal,
}
