package worker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculatePeriod_Daily(t *testing.T) {
	// 2 AM UTC on Jan 15, 2025
	now := time.Date(2025, 1, 15, 2, 0, 0, 0, time.UTC)

	start, end := CalculatePeriod(now, "DAILY", 0)

	expectedStart := time.Date(2025, 1, 14, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedStart, start, "daily period should start at previous midnight")
	assert.Equal(t, expectedEnd, end, "daily period should end at today's midnight")
}

func TestCalculatePeriod_EndOfDay(t *testing.T) {
	now := time.Date(2025, 1, 15, 23, 30, 0, 0, time.UTC)

	start, end := CalculatePeriod(now, "END_OF_DAY", 0)

	expectedStart := time.Date(2025, 1, 14, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedStart, start)
	assert.Equal(t, expectedEnd, end)
}

func TestCalculatePeriod_Weekly(t *testing.T) {
	now := time.Date(2025, 1, 15, 2, 0, 0, 0, time.UTC)

	start, end := CalculatePeriod(now, "WEEKLY", 0)

	expectedStart := time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedStart, start, "weekly period should start 7 days before today")
	assert.Equal(t, expectedEnd, end, "weekly period should end at today's midnight")
}

func TestCalculatePeriod_Monthly(t *testing.T) {
	now := time.Date(2025, 3, 5, 2, 0, 0, 0, time.UTC)

	start, end := CalculatePeriod(now, "MONTHLY", 0)

	expectedStart := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedStart, start, "monthly period should start at first of previous month")
	assert.Equal(t, expectedEnd, end, "monthly period should end at first of current month")
}

func TestCalculatePeriod_Monthly_January(t *testing.T) {
	// January should roll back to December of the previous year
	now := time.Date(2025, 1, 3, 2, 0, 0, 0, time.UTC)

	start, end := CalculatePeriod(now, "MONTHLY", 0)

	expectedStart := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, expectedStart, start)
	assert.Equal(t, expectedEnd, end)
}

func TestCalculatePeriod_WithOffset(t *testing.T) {
	now := time.Date(2025, 1, 15, 2, 0, 0, 0, time.UTC)
	offset := 6 * time.Hour

	start, end := CalculatePeriod(now, "DAILY", offset)

	expectedStart := time.Date(2025, 1, 14, 20, 0, 0, 0, time.UTC)
	expectedEnd := now

	assert.Equal(t, expectedStart, start, "offset should subtract from current time")
	assert.Equal(t, expectedEnd, end, "end should be current time")
}

func TestCalculatePeriod_DefaultFallback(t *testing.T) {
	now := time.Date(2025, 1, 15, 2, 0, 0, 0, time.UTC)

	start, end := CalculatePeriod(now, "UNKNOWN_TYPE", 0)

	expectedStart := now.Add(-24 * time.Hour)
	expectedEnd := now

	assert.Equal(t, expectedStart, start, "unknown type should default to last 24 hours")
	assert.Equal(t, expectedEnd, end)
}

func TestCalculatePeriod_ConvertsToUTC(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	now := time.Date(2025, 1, 15, 2, 0, 0, 0, est)

	start, end := CalculatePeriod(now, "DAILY", 0)

	assert.Equal(t, time.UTC, start.Location(), "start should be UTC")
	assert.Equal(t, time.UTC, end.Location(), "end should be UTC")
}
