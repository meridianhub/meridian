-- Settlement Finality: Re-add constraints with FINALIZED and FINAL values
-- Split from 20260209000003 for CockroachDB compatibility.

ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_status"
  CHECK ("status" IN ('PENDING', 'RUNNING', 'COMPLETED', 'FINALIZED', 'FAILED', 'CANCELLED'));

ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_settlement_type"
  CHECK ("settlement_type" IN ('DAILY', 'WEEKLY', 'MONTHLY', 'ON_DEMAND', 'END_OF_DAY', 'REAL_TIME', 'FINAL'));

CREATE INDEX "idx_settlement_run_finalized" ON "settlement_run" ("status")
  WHERE "status" = 'FINALIZED';
