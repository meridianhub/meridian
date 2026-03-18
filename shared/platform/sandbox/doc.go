// Package sandbox provides unified Starlark sandbox security configuration
// and thread hardening for all Meridian runtimes (saga, valuation, forecasting).
//
// [Config] holds security constraints (timeout, max script size, max execution steps,
// memory threshold). [HardenThread] applies the step limit to a Starlark thread.
// [MemoryMonitor] polls heap allocation during execution and cancels the context when
// the threshold is exceeded.
//
// All Meridian runtimes that execute tenant-supplied Starlark scripts must use this
// package to ensure consistent security properties across services.
package sandbox
