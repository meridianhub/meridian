-- Measurements table for storing measurement audit trail
-- Supports RecordMeasurement endpoint (Task 35)
-- Uses unqualified table names (relies on database-per-service architecture per ADR-002)

-- Create "measurement" table (singular, unqualified)
CREATE TABLE "measurement" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "created_by" character varying(100) NOT NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  "updated_by" character varying(100) NOT NULL,
  "deleted_at" timestamptz NULL,
  "financial_position_log_id" uuid NOT NULL,
  "measurement_type" character varying(50) NOT NULL,
  "value" decimal(38, 18) NOT NULL,
  "unit" character varying(20) NOT NULL,
  "timestamp" timestamptz NOT NULL,
  "metadata" jsonb NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_measurement_financial_position_log" FOREIGN KEY ("financial_position_log_id") REFERENCES "financial_position_log" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);

-- Create indexes for measurement
CREATE INDEX "idx_measurement_position_state_timestamp" ON "measurement" ("financial_position_log_id", "timestamp");
CREATE INDEX "idx_measurement_created_at" ON "measurement" ("created_at");
CREATE INDEX "idx_measurement_deleted_at" ON "measurement" ("deleted_at");
CREATE INDEX "idx_measurement_type" ON "measurement" ("measurement_type");

-- Add validation constraint for common measurement types
ALTER TABLE "measurement"
  ADD CONSTRAINT "chk_measurement_type"
  CHECK ("measurement_type" IN ('kWh', 'GPU-Hours', 'CPU-Hours', 'Storage-GB', 'Bandwidth-GB', 'Carbon-Tonnes', 'Water-Litres', 'Custom'));
