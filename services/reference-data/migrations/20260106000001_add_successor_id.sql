-- Add successor_id column for instrument lineage tracking
-- When an instrument is DEPRECATED, it can optionally point to its successor (replacement) instrument.
-- This enables clients to follow the lineage chain to find the current active instrument.

-- Add successor_id column (nullable - not all deprecated instruments have successors)
ALTER TABLE "instrument_definition"
  ADD COLUMN "successor_id" uuid NULL;

-- Add self-referential foreign key constraint
ALTER TABLE "instrument_definition"
  ADD CONSTRAINT "fk_instrument_definition_successor"
  FOREIGN KEY ("successor_id") REFERENCES "instrument_definition" ("id");

-- Index for efficient lineage traversal (finding all instruments that point to a given successor)
CREATE INDEX "idx_instrument_definition_successor_id" ON "instrument_definition" ("successor_id")
  WHERE "successor_id" IS NOT NULL;

-- Update the lifecycle trigger to enforce successor_id validation rules
CREATE OR REPLACE FUNCTION "enforce_instrument_lifecycle"()
RETURNS TRIGGER AS $$
DECLARE
  successor_record RECORD;
BEGIN
  -- Always update updated_at on any change
  NEW."updated_at" = NOW();

  -- Enforce write-once semantics for successor_id
  -- Once set, successor_id cannot be changed (regardless of status)
  IF OLD."successor_id" IS NOT NULL AND OLD."successor_id" IS DISTINCT FROM NEW."successor_id" THEN
    RAISE EXCEPTION 'Cannot modify successor_id once set (write-once semantics)';
  END IF;

  -- Allow all edits when status is DRAFT
  IF OLD."status" = 'DRAFT' THEN
    -- If transitioning from DRAFT to ACTIVE, set activated_at
    IF NEW."status" = 'ACTIVE' THEN
      NEW."activated_at" = NOW();
    -- If transitioning from DRAFT to DEPRECATED, set deprecated_at
    ELSIF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();
    END IF;
    RETURN NEW;
  END IF;

  -- For ACTIVE or DEPRECATED instruments, certain fields become immutable
  IF OLD."status" IN ('ACTIVE', 'DEPRECATED') THEN
    -- Prevent changes to immutable fields (validation rules that affect ledger integrity)
    IF OLD."validation_expression" IS DISTINCT FROM NEW."validation_expression" THEN
      RAISE EXCEPTION 'Cannot modify validation_expression for % instrument', OLD."status";
    END IF;
    IF OLD."fungibility_key_expression" IS DISTINCT FROM NEW."fungibility_key_expression" THEN
      RAISE EXCEPTION 'Cannot modify fungibility_key_expression for % instrument', OLD."status";
    END IF;
    IF OLD."error_message_expression" IS DISTINCT FROM NEW."error_message_expression" THEN
      RAISE EXCEPTION 'Cannot modify error_message_expression for % instrument', OLD."status";
    END IF;
  END IF;

  -- Prevent invalid status transitions
  IF OLD."status" = 'ACTIVE' THEN
    IF NEW."status" = 'DRAFT' THEN
      RAISE EXCEPTION 'Cannot transition from ACTIVE back to DRAFT';
    END IF;
    -- Allow ACTIVE to DEPRECATED, set deprecated_at and validate successor
    IF NEW."status" = 'DEPRECATED' THEN
      NEW."deprecated_at" = NOW();

      -- If successor_id is provided, validate it
      IF NEW."successor_id" IS NOT NULL THEN
        -- Look up the successor instrument
        SELECT "id", "status", "dimension"
        INTO successor_record
        FROM "instrument_definition"
        WHERE "id" = NEW."successor_id";

        -- Successor must exist
        IF successor_record.id IS NULL THEN
          RAISE EXCEPTION 'Successor instrument does not exist: %', NEW."successor_id";
        END IF;

        -- Successor must be ACTIVE
        IF successor_record.status != 'ACTIVE' THEN
          RAISE EXCEPTION 'Successor instrument must be ACTIVE, but is %', successor_record.status;
        END IF;

        -- Successor must have same dimension
        IF successor_record.dimension != NEW."dimension" THEN
          RAISE EXCEPTION 'Successor instrument dimension (%) must match current instrument dimension (%)',
            successor_record.dimension, NEW."dimension";
        END IF;
      END IF;
    END IF;
  END IF;

  IF OLD."status" = 'DEPRECATED' THEN
    IF NEW."status" IN ('DRAFT', 'ACTIVE') THEN
      RAISE EXCEPTION 'Cannot transition from DEPRECATED to %', NEW."status";
    END IF;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
