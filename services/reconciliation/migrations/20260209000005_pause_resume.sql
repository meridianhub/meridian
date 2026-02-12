-- Pause/Resume: Add PAUSED status and last_completed_phase checkpoint column.

-- Add PAUSED to settlement_run status constraint
ALTER TABLE "settlement_run" DROP CONSTRAINT "chk_settlement_run_status";
ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_status"
  CHECK ("status" IN ('PENDING', 'RUNNING', 'PAUSED', 'COMPLETED', 'FINALIZED', 'FAILED', 'CANCELLED'));

-- Add checkpoint column for pipeline resumption
ALTER TABLE "settlement_run"
  ADD COLUMN "last_completed_phase" character varying(30) NULL;

ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_last_completed_phase"
  CHECK ("last_completed_phase" IS NULL OR "last_completed_phase" IN (
    'SNAPSHOT_CAPTURE', 'VARIANCE_DETECTION', 'VARIANCE_VALUATION', 'BALANCE_ASSERTION'
  ));
