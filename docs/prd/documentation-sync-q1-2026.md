# PRD: Documentation Sync Q1 2026

**Author:** Engineering
**Status:** Draft
**Created:** 2026-01-01
**Target:** Q1 2026

---

## Executive Summary

Following the service client library migration (consumer-owned → service-owned pattern), documentation across the codebase references outdated directory structures and import paths. This PRD tracks the cleanup work to bring documentation in sync with the current codebase.

---

## Goals

1. **Accuracy**: All documentation reflects current code structure
2. **Discoverability**: Developers can find correct import paths from docs
3. **Maintainability**: Remove stale references that cause confusion

---

## Non-Goals

- Writing new documentation
- Architectural changes
- Code refactoring

---

## Work Items

### 1.1 Main README.md - Directory Tree

**Problem:** Shows old `clients/` (plural) directory in project structure.

**File:** `README.md:30`

**Current:**
```
├── services/
│   ├── current-account/
│   │   ├── clients/             ← OLD
```

**Should Be:**
```
├── services/
│   ├── current-account/
│   │   ├── client/              ← Service-owned client
```

**Estimated Effort:** 15 min

---

### 1.2 Circuit Breaker Usage Guide

**Problem:** References old import paths and directory structure.

**File:** `docs/guides/circuit-breaker-usage.md`

**Lines Affected:**
- Line 5: Says location is `services/{service}/clients/`
- Line 26: Old import path `internal/current-account/clients`

**Should Reference:**
- `shared/pkg/clients/` for circuit breaker utilities
- `services/{service}/client/` for service-owned clients

**Estimated Effort:** 30 min

---

### 1.3 New BIAN Service Checklist

**Problem:** References old client directory pattern.

**File:** `docs/guides/new-bian-service-checklist.md`

**Lines Affected:**
- Line 782: `services/{service}/clients/` → `services/{service}/client/`
- Line 788: `services/current-account/clients/` → `services/current-account/client/`

**Estimated Effort:** 15 min

---

### 1.4 Service Coupling Analysis

**Problem:** Multiple references to old `internal/` directory structure that no longer exists.

**File:** `docs/architecture/service-coupling-analysis.md`

**Lines Affected:**
- Lines 147-149: `internal/current-account/clients/*.go`
- Lines 328-331: Similar stale paths
- Lines 420-422: Similar stale paths
- Line 506: Similar stale paths

**Note:** Document dated 2025-11-19. Consider adding "Historical Analysis" header or updating paths entirely.

**Estimated Effort:** 45 min

---

### 1.5 Position Keeping doc.go - Stale Future Work

**Problem:** Package doc says "Future PRs will add" for methods already implemented.

**File:** `services/position-keeping/service/doc.go:25-33`

**Current:**
```go
// Future PRs will add:
//   - UpdateFinancialPositionLog: Update an existing log (Part 2)
//   - ControlFinancialPositionLog: Manage log lifecycle (Part 2)
//   - BulkImportTransactions: Import multiple transactions (Part 3)
```

**Reality:**
| Method | Status | Location |
|--------|--------|----------|
| `UpdateFinancialPositionLog` | ✅ Implemented | `service/update.go` |
| `BulkImportTransactions` | ✅ Implemented | Proto + service |
| `ControlFinancialPositionLog` | ❌ Not implemented | Keep as future |

**Should Be:**
```go
// Implemented operations:
//   - InitiateFinancialPositionLog: Create a new financial position log
//   - RetrieveFinancialPositionLog: Fetch a log by ID
//   - ListFinancialPositionLogs: Query logs with filtering and pagination
//   - UpdateFinancialPositionLog: Update an existing log
//   - BulkImportTransactions: Import multiple transactions
//
// Future work:
//   - ControlFinancialPositionLog: Manage log lifecycle
```

**Estimated Effort:** 15 min

---

### 1.6 Audit for Other Stale References

**Problem:** There may be additional stale references not yet identified.

**Search Patterns:**
```bash
# Find references to old clients/ pattern
grep -r "services/.*/clients/" docs/ README.md --include="*.md"

# Find references to old internal/ structure
grep -r "internal/current-account" docs/ --include="*.md"
grep -r "internal/financial-accounting" docs/ --include="*.md"

# Find "will be implemented" or "future PR" that may be stale
grep -ri "will be implemented\|future pr\|todo.*implement" services/*/service/doc.go
```

**Acceptance Criteria:**
- [ ] Run search patterns above
- [ ] Document any additional findings
- [ ] Fix or create follow-up tasks

**Estimated Effort:** 30 min

---

## Summary Table

| ID | Work Item | File | Effort |
|----|-----------|------|--------|
| 1.1 | README directory tree | `README.md` | 15m |
| 1.2 | Circuit breaker guide | `docs/guides/circuit-breaker-usage.md` | 30m |
| 1.3 | BIAN service checklist | `docs/guides/new-bian-service-checklist.md` | 15m |
| 1.4 | Service coupling analysis | `docs/architecture/service-coupling-analysis.md` | 45m |
| 1.5 | Position keeping doc.go | `services/position-keeping/service/doc.go` | 15m |
| 1.6 | Audit for other references | Various | 30m |

**Total Estimated Effort:** 2.5 hours

---

## Success Metrics

1. **Zero stale paths**: `grep -r "clients/" docs/` returns no hits for old pattern
2. **Accurate doc.go**: All package docs reflect implemented methods
3. **Import paths valid**: All documented import paths compile

---

## Verification

After fixes, run:
```bash
# Verify no old clients/ references remain
! grep -r "services/.*/clients/" docs/ README.md --include="*.md"

# Verify documented imports are valid
go build ./...
```
