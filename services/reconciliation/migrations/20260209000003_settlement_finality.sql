-- Settlement Finality: Add FINALIZED status and FINAL settlement type

-- Add FINALIZED to settlement_run status constraint
ALTER TABLE "settlement_run" DROP CONSTRAINT "chk_settlement_run_status";
ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_status"
  CHECK ("status" IN ('PENDING', 'RUNNING', 'COMPLETED', 'FINALIZED', 'FAILED', 'CANCELLED'));

-- Add FINAL to settlement_run settlement_type constraint
ALTER TABLE "settlement_run" DROP CONSTRAINT "chk_settlement_run_settlement_type";
ALTER TABLE "settlement_run"
  ADD CONSTRAINT "chk_settlement_run_settlement_type"
  CHECK ("settlement_type" IN ('DAILY', 'WEEKLY', 'MONTHLY', 'ON_DEMAND', 'END_OF_DAY', 'REAL_TIME', 'FINAL'));

-- Add index for finalized runs for efficient querying
CREATE INDEX "idx_settlement_run_finalized" ON "settlement_run" ("status")
  WHERE "status" = 'FINALIZED';
