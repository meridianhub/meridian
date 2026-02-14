-- Pause/Resume: Drop old status constraint, add checkpoint column.
-- Constraint re-add is in 20260209000005b for CockroachDB compatibility.

ALTER TABLE "settlement_run" DROP CONSTRAINT "chk_settlement_run_status";

-- Add checkpoint column for pipeline resumption
ALTER TABLE "settlement_run"
  ADD COLUMN "last_completed_phase" character varying(30) NULL;
