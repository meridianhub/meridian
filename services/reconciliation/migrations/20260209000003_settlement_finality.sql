-- Settlement Finality: Drop old constraints
-- CockroachDB cannot drop and re-add a constraint with the same name
-- in a single transaction, so the re-add is in 20260209000003b.

ALTER TABLE "settlement_run" DROP CONSTRAINT "chk_settlement_run_status";
ALTER TABLE "settlement_run" DROP CONSTRAINT "chk_settlement_run_settlement_type";
