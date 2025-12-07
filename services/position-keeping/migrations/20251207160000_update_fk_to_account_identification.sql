-- Update FK constraints to reference account_identification after current-account column rename
-- The original migration (20251107160248) references account_number which was renamed
-- to account_identification in current-account migration 20251204143000

-- Drop and recreate FK for financial_position_logs
ALTER TABLE "position_keeping"."financial_position_logs"
  DROP CONSTRAINT "fk_position_keeping_financial_position_logs_account";

ALTER TABLE "position_keeping"."financial_position_logs"
  ADD CONSTRAINT "fk_position_keeping_financial_position_logs_account"
  FOREIGN KEY ("account_id") REFERENCES "current_account"."accounts" ("account_identification")
  ON UPDATE NO ACTION ON DELETE RESTRICT;

-- Drop and recreate FK for transaction_log_entries
ALTER TABLE "position_keeping"."transaction_log_entries"
  DROP CONSTRAINT "fk_position_keeping_transaction_log_entries_account";

ALTER TABLE "position_keeping"."transaction_log_entries"
  ADD CONSTRAINT "fk_position_keeping_transaction_log_entries_account"
  FOREIGN KEY ("account_id") REFERENCES "current_account"."accounts" ("account_identification")
  ON UPDATE NO ACTION ON DELETE RESTRICT;
