-- Pause/Resume: Re-add status constraint with PAUSED, add phase constraint.
-- Split from 20260209000005 for CockroachDB compatibility.

ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_status"
  CHECK ("status" IN ('PENDING', 'RUNNING', 'PAUSED', 'COMPLETED', 'FINALIZED', 'FAILED', 'CANCELLED'));

ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_last_completed_phase"
  CHECK ("last_completed_phase" IS NULL OR "last_completed_phase" IN (
    'SNAPSHOT_CAPTURE', 'VARIANCE_DETECTION', 'VARIANCE_VALUATION', 'BALANCE_ASSERTION'
  ));
