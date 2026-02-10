# Task 11 Implementation Notes

## Understanding the Requirement

After analysis, task 11 is about ensuring valuation_analysis flows through to Position Keeping when transactions occur.

### Current Architecture

**Withdrawal/Deposit Sagas** → Call `position_keeping.initiate_log` directly → Position Entry created

**Lien Flow** (for payments):

1. InitiateLien → Atomic valuation happens → RecordReservation (Position Keeping)
2. ExecuteLien → ReleaseReservation (Position Keeping)

### The Gap

Currently, the valuation_analysis computed during atomic valuation is NOT stored in Position Keeping's
position entries. This prevents:

- Audit trails showing how valuations were computed
- Reconciliation of degraded mode entries
- Regulatory transparency

### Implementation Plan

#### Subtask 11.3: Update Position Keeping to accept and store valuation_analysis

1. Add optional `valuation_analysis` parameter to `shared/pkg/saga/schema/handlers.yaml`:

   ```yaml
   position_keeping.initiate_log:
     params:
       # ... existing params ...
       valuation_analysis:
         type: struct
         required: false
         description: "Optional valuation analysis metadata (from atomic valuation)"
   ```

2. Update Position Keeping saga handler (services/position-keeping/service/saga_handlers.go):
   - Accept valuation_analysis from saga input
   - Store it in Position.Attributes as JSON

3. Test file created: `services/position-keeping/service/valuation_analysis_storage_test.go`
   - Verifies valuation_analysis storage
   - Tests degraded mode query
   - Ensures backward compatibility

#### Subtask 11.1-11.2: NOT needed for basic flow

The withdrawal saga does NOT need to use InitiateLien. The current direct
`position_keeping.initiate_log` call is fine for same-currency withdrawals.

For multi-asset withdrawals that NEED valuation:

- They would use the lien flow (InitiateLien + ExecuteLien)
- This is already implemented in task 10
- Withdrawal saga can remain simple for now

#### Subtask 11.4: Saga checkpoint preservation

- Starlark sagas automatically checkpoint step results
- No special handling needed if valuation_analysis is part of step output

#### Subtask 11.5: Reconciliation script

Create `scripts/reconcile_degraded_valuations.sql` to query degraded entries

## Next Steps

1. Update handlers.yaml to add valuation_analysis parameter
2. Update Position Keeping saga handler to store valuation_analysis in attributes
3. Run tests to verify
4. Create reconciliation query script
5. Update task status and create PR
