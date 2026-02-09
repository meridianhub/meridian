package worker

import "time"

// CalculatePeriod computes the reconciliation period window based on the
// settlement type and the current time. The period is always calculated in UTC.
//
// For example, a DAILY settlement at 2 AM UTC would produce:
//   - PeriodStart: yesterday 00:00:00 UTC
//   - PeriodEnd: today 00:00:00 UTC
func CalculatePeriod(now time.Time, settlementType string, offset time.Duration) (start, end time.Time) {
	now = now.UTC()

	if offset > 0 {
		return now.Add(-offset), now
	}

	switch settlementType {
	case "DAILY", "END_OF_DAY":
		// Previous day: midnight to midnight
		todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return todayMidnight.Add(-24 * time.Hour), todayMidnight
	case "WEEKLY":
		// Previous 7 days ending at midnight today
		todayMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return todayMidnight.Add(-7 * 24 * time.Hour), todayMidnight
	case "MONTHLY":
		// Previous month: first day to first day of current month
		firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		firstOfPrevMonth := firstOfMonth.AddDate(0, -1, 0)
		return firstOfPrevMonth, firstOfMonth
	default:
		// Default: last 24 hours
		return now.Add(-24 * time.Hour), now
	}
}
