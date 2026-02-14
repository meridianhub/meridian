package worker

import (
	"errors"
	"time"
)

// Billing worker errors shared across billing scheduler adapters and dunning worker.
var (
	ErrNilBillingRepo   = errors.New("billing repository is required")
	ErrNilRedisClient   = errors.New("redis client is required")
	ErrNilBillingLogger = errors.New("logger is required")
)

// NowFunc returns the current time. Replaceable for testing.
var NowFunc = func() time.Time { return time.Now().UTC() }

// IdempotencyKeyTTL is the TTL for billing run idempotency keys in Redis.
const IdempotencyKeyTTL = 48 * time.Hour

// BillingSchedulerConfig holds configuration for the billing scheduler.
type BillingSchedulerConfig struct {
	// TenantID is the tenant this scheduler runs billing for.
	TenantID string
	// CronExpression is the billing schedule (e.g., "0 2 1 * *" for 2 AM on 1st of month).
	CronExpression string
	// ShadowMode when true creates DRAFT invoices without initiating payment.
	ShadowMode bool
}

// calculateBillingPeriod returns the previous calendar month as the billing period.
// For a billing run at any point in month M, the period covers month M-1.
func calculateBillingPeriod(now time.Time) (time.Time, time.Time) {
	// Start of current month
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	// Previous month start
	periodStart := currentMonthStart.AddDate(0, -1, 0)
	// Previous month end = current month start
	periodEnd := currentMonthStart
	return periodStart, periodEnd
}
