-- Constraints and indexes for enhanced party_association (columns must be public first)
ALTER TABLE "party_association" ADD CONSTRAINT "chk_party_association_status"
  CHECK ("status" IN ('ACTIVE', 'SUSPENDED', 'TERMINATED'));
ALTER TABLE "party_association" ADD CONSTRAINT "chk_party_association_validity_range"
  CHECK ("effective_to" IS NULL OR "effective_to" > "effective_from");
CREATE INDEX "idx_party_association_status" ON "party_association" ("status") WHERE "status" = 'ACTIVE';
