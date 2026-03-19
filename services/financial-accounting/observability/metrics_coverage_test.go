package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestRecordClearingAccountCacheHit(t *testing.T) {
	initial := testutil.ToFloat64(clearingAccountCacheHits)

	RecordClearingAccountCacheHit()

	newCount := testutil.ToFloat64(clearingAccountCacheHits)
	assert.Equal(t, initial+1, newCount, "cache hit counter should increment by 1")
}

func TestRecordClearingAccountCacheMiss(t *testing.T) {
	initial := testutil.ToFloat64(clearingAccountCacheMisses)

	RecordClearingAccountCacheMiss()

	newCount := testutil.ToFloat64(clearingAccountCacheMisses)
	assert.Equal(t, initial+1, newCount, "cache miss counter should increment by 1")
}

func TestRecordClearingAccountLookupDuration(_ *testing.T) {
	// Verify it doesn't panic when recording a duration
	RecordClearingAccountLookupDuration(50 * time.Millisecond)
	RecordClearingAccountLookupDuration(200 * time.Millisecond)
}

func TestRecordClearingAccountLookupError(t *testing.T) {
	initial := testutil.ToFloat64(clearingAccountLookupErrors.WithLabelValues("deposit"))

	RecordClearingAccountLookupError("deposit")

	newCount := testutil.ToFloat64(clearingAccountLookupErrors.WithLabelValues("deposit"))
	assert.Equal(t, initial+1, newCount, "lookup error counter should increment by 1")
}

func TestRecordResolverFallback(t *testing.T) {
	initial := testutil.ToFloat64(resolverFallbackTotal.WithLabelValues("GBP", "deposit"))

	RecordResolverFallback("GBP", "deposit")

	newCount := testutil.ToFloat64(resolverFallbackTotal.WithLabelValues("GBP", "deposit"))
	assert.Equal(t, initial+1, newCount, "resolver fallback counter should increment by 1")
}
