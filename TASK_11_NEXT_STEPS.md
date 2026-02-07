# Task 11 - Next Steps for Completion

## Current State

**Completed:**

- ✅ Added `valuation_analysis` parameter to `shared/pkg/saga/schema/handlers.yaml`
- ✅ Documented architecture in `TASK_11_IMPLEMENTATION_NOTES.md`

**In Progress:**

- 🔄 Need to implement actual storage of valuation_analysis in Position Keeping

## Implementation Path

### 1. Position Keeping Service Changes

The challenge: Position entries are created via `InitiateFinancialPositionLog` which accepts an `InitialEntry`.
The `InitialEntry` doesn't have attributes currently.

**Options:**

#### Option A: Store valuation_analysis in Position.Attributes (Recommended)

1. Position domain already has `Attributes map[string]string`
2. Modify `currentAccountPositionKeepingInitiateLog` handler (services/current-account/service/saga_handlers.go:191):
   - Extract optional `valuation_analysis` from params
   - Marshal to JSON string
   - Add to position attributes when calling Position Keeping

3. Position Keeping service (services/position-keeping/service/initiate.go):
   - Accept attributes parameter or derive from transaction log entry
   - Store in Position.Attributes field

#### Option B: Add attributes to TransactionLogEntry proto (Alternative)

1. Modify `api/proto/meridian/position_keeping/v1/position_keeping.proto`
2. Add `map<string, string> attributes` to `TransactionLogEntry` message
3. Regenerate protos
4. Update Position Keeping service to extract and store attributes

### 2. Saga Handler Updates

In `services/current-account/service/saga_handlers.go`:

```go
func currentAccountPositionKeepingInitiateLog(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
    // ... existing code ...

    // NEW: Extract optional valuation_analysis
    var valuationAnalysisJSON string
    if valuationAnalysis, ok := params["valuation_analysis"]; ok && valuationAnalysis != nil {
        // Marshal to JSON for storage
        bytes, err := json.Marshal(valuationAnalysis)
        if err != nil {
            deps.Logger.Warn("failed to marshal valuation_analysis", "error", err)
        } else {
            valuationAnalysisJSON = string(bytes)
        }
    }

    // Call Position Keeping with attributes
    // (implementation depends on which option chosen above)
}
```

### 3. Reconciliation Query Script

Create `scripts/reconcile_degraded_valuations.sql`:

```sql
-- Find all position entries with degraded valuation analysis
SELECT
    p.id,
    p.account_id,
    p.instrument_code,
    p.amount,
    p.created_at,
    p.attributes->'valuation_analysis'->>'method_id' as method_id,
    p.attributes->'valuation_analysis'->>'degraded_mode' as degraded_mode,
    p.attributes->'valuation_analysis'->>'degraded_reason' as degraded_reason
FROM positions p
WHERE
    p.attributes->'valuation_analysis'->>'degraded_mode' = 'true'
ORDER BY p.created_at DESC;
```

### 4. Testing

1. Update or create integration tests in Position Keeping
2. Test saga flow with valuation_analysis parameter
3. Verify attributes are stored and queryable

## Recommended Next Session Actions

1. Choose implementation path (Option A recommended - simpler)
2. Implement Position Keeping attribute storage
3. Update saga handler to marshal and pass valuation_analysis
4. Create reconciliation query script
5. Write integration test
6. Run full test suite
7. Create PR

## Key Files to Modify

- `services/current-account/service/saga_handlers.go` (handler implementation)
- `services/position-keeping/service/initiate.go` (or wherever positions are created)
- `services/position-keeping/domain/position.go` (if needed)
- `scripts/reconcile_degraded_valuations.sql` (new file)

## Context Preservation

This task is about enabling audit trails for valuation calculations. The valuation_analysis struct
contains method_id, applied_rates, degraded_mode, etc. - all critical for regulatory transparency.

**Not** about changing the saga flow to use liens (that's already done for payments).
**Not** about removing EvaluateAssetValuation (it's inquiry-only, doesn't conflict with atomic valuation).

The key is: when atomic valuation happens (e.g., in InitiateLien), pass the resulting valuation_analysis
through to Position Keeping so it's stored for audit purposes.
