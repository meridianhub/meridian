// Package scheduler provides cron-based and catch-up job scheduling for tenant workflows.
//
// The [Runner] executes tenant-specific schedules obtained from a [ScheduleProvider]
// (e.g., Reference Data). Each tick calls [Executor.Execute] with the triggered schedule.
// Only the leader pod runs the scheduler; leader election is handled externally via
// the [redislock] package.
//
// On startup, the catch-up logic re-executes any missed windows within the configured
// MaxCatchUpAge. Windows older than that threshold are recorded as MISSED for audit
// purposes without re-execution.
//
// # Infrastructure package
//
// This is a platform-level package with no domain semantics.
// Domain-specific schedule logic belongs in the calling service.
package scheduler
