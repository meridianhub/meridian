-- Position keeping test schema DDL
-- Embedded via //go:embed for test helper loadSchema()

CREATE SCHEMA IF NOT EXISTS position_keeping;

CREATE TABLE position_keeping.financial_position_log (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by character varying(100) NOT NULL,
    deleted_at timestamptz NULL,
    log_id uuid NOT NULL,
    account_id character varying(34) NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    current_status character varying(20) NOT NULL,
    previous_status character varying(20) NULL,
    status_updated_at timestamptz NOT NULL,
    status_reason text NOT NULL,
    failure_reason text NULL,
    reconciliation_status character varying(20) NOT NULL,
    opening_balance_amount decimal(38, 18) NOT NULL DEFAULT 0,
    opening_balance_currency character(3) NOT NULL DEFAULT 'GBP',
    opening_balance_recorded_at timestamptz NULL,
    PRIMARY KEY (id)
);

CREATE UNIQUE INDEX idx_position_keeping_financial_position_log_log_id
ON position_keeping.financial_position_log (log_id);

CREATE INDEX idx_financial_position_log_account_id ON position_keeping.financial_position_log (account_id);
CREATE INDEX idx_financial_position_log_current_status ON position_keeping.financial_position_log (current_status);
CREATE INDEX idx_financial_position_log_deleted_at ON position_keeping.financial_position_log (deleted_at);

ALTER TABLE position_keeping.financial_position_log
    ADD CONSTRAINT chk_financial_position_log_current_status
    CHECK (current_status IN ('PENDING', 'RECONCILED', 'POSTED', 'CANCELLED', 'FAILED', 'REJECTED', 'AMENDED', 'REVERSED'));

ALTER TABLE position_keeping.financial_position_log
    ADD CONSTRAINT chk_financial_position_log_reconciliation_status
    CHECK (reconciliation_status IN ('UNRECONCILED', 'MATCHED', 'MISMATCHED', 'RESOLVED'));

CREATE TABLE position_keeping.transaction_log_entry (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by character varying(100) NOT NULL,
    deleted_at timestamptz NULL,
    entry_id uuid NOT NULL,
    financial_position_log_id uuid NOT NULL,
    transaction_id uuid NOT NULL,
    account_id character varying(34) NOT NULL,
    amount_cents bigint NOT NULL,
    currency character(3) NOT NULL DEFAULT 'GBP',
    direction character varying(10) NOT NULL,
    timestamp timestamptz NOT NULL,
    description text NULL,
    reference character varying(100) NULL,
    source character varying(50) NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT fk_transaction_log_entry_financial_position_log
        FOREIGN KEY (financial_position_log_id)
        REFERENCES position_keeping.financial_position_log(id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_transaction_log_entry_entry_id ON position_keeping.transaction_log_entry (entry_id);
CREATE INDEX idx_transaction_log_entry_deleted_at ON position_keeping.transaction_log_entry (deleted_at);
CREATE INDEX idx_transaction_log_entry_log_id ON position_keeping.transaction_log_entry (financial_position_log_id);
CREATE INDEX idx_transaction_log_entry_timestamp ON position_keeping.transaction_log_entry (timestamp);
CREATE INDEX idx_transaction_log_entry_transaction_id ON position_keeping.transaction_log_entry (transaction_id);

ALTER TABLE position_keeping.transaction_log_entry
    ADD CONSTRAINT chk_transaction_log_entry_currency
    CHECK (char_length(currency) = 3);

ALTER TABLE position_keeping.transaction_log_entry
    ADD CONSTRAINT chk_transaction_log_entry_direction
    CHECK (direction IN ('DEBIT', 'CREDIT'));

CREATE TABLE position_keeping.transaction_lineage (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by character varying(100) NOT NULL,
    deleted_at timestamptz NULL,
    financial_position_log_id uuid NOT NULL,
    transaction_id uuid NOT NULL,
    parent_transaction_id uuid NULL,
    child_transaction_ids jsonb NOT NULL DEFAULT '[]',
    related_transaction_ids jsonb NOT NULL DEFAULT '[]',
    transaction_type character varying(50) NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT fk_transaction_lineage_financial_position_log
        FOREIGN KEY (financial_position_log_id)
        REFERENCES position_keeping.financial_position_log(id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_transaction_lineage_log_id ON position_keeping.transaction_lineage (financial_position_log_id);
CREATE INDEX idx_transaction_lineage_deleted_at ON position_keeping.transaction_lineage (deleted_at);
CREATE INDEX idx_transaction_lineage_parent_id ON position_keeping.transaction_lineage (parent_transaction_id);
CREATE INDEX idx_transaction_lineage_transaction_id ON position_keeping.transaction_lineage (transaction_id);

CREATE TABLE position_keeping.audit_trail_entry (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by character varying(100) NOT NULL,
    deleted_at timestamptz NULL,
    audit_id uuid NOT NULL,
    financial_position_log_id uuid NOT NULL,
    timestamp timestamptz NOT NULL,
    user_id character varying(100) NOT NULL,
    action character varying(100) NOT NULL,
    details text NULL,
    ip_address character varying(45) NULL,
    system_context jsonb NULL,
    PRIMARY KEY (id),
    CONSTRAINT fk_audit_trail_entry_financial_position_log
        FOREIGN KEY (financial_position_log_id)
        REFERENCES position_keeping.financial_position_log(id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_audit_trail_entry_audit_id ON position_keeping.audit_trail_entry (audit_id);
CREATE INDEX idx_audit_trail_entry_deleted_at ON position_keeping.audit_trail_entry (deleted_at);
CREATE INDEX idx_audit_trail_entry_log_id ON position_keeping.audit_trail_entry (financial_position_log_id);
CREATE INDEX idx_audit_trail_entry_timestamp ON position_keeping.audit_trail_entry (timestamp);
CREATE INDEX idx_audit_trail_entry_user_id ON position_keeping.audit_trail_entry (user_id);

CREATE TABLE position_keeping.position (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by character varying(100) NOT NULL,
    deleted_at timestamptz NULL,
    account_id character varying(34) NOT NULL,
    instrument_code character varying(32) NOT NULL,
    bucket_key character varying(256) NOT NULL,
    amount decimal(38, 18) NOT NULL,
    dimension character varying(32) NOT NULL DEFAULT 'Monetary',
    attributes jsonb NULL,
    reference_id uuid NULL,
    PRIMARY KEY (id)
);

CREATE INDEX idx_position_account_id ON position_keeping.position (account_id);
CREATE INDEX idx_position_aggregation ON position_keeping.position (account_id, instrument_code, bucket_key);
CREATE INDEX idx_position_deleted_at ON position_keeping.position (deleted_at);
CREATE INDEX idx_position_active ON position_keeping.position (account_id, instrument_code, bucket_key)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_position_reference_id ON position_keeping.position (reference_id);
CREATE INDEX idx_position_created_at ON position_keeping.position (created_at);

CREATE OR REPLACE FUNCTION position_keeping.positions_append_only()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.amount IS DISTINCT FROM NEW.amount THEN
        RAISE EXCEPTION 'positions table is append-only - UPDATE on amount column is forbidden'
            USING ERRCODE = 'P0001';
    END IF;
    IF OLD.account_id IS DISTINCT FROM NEW.account_id THEN
        RAISE EXCEPTION 'positions table is append-only - UPDATE on account_id column is forbidden'
            USING ERRCODE = 'P0001';
    END IF;
    IF OLD.instrument_code IS DISTINCT FROM NEW.instrument_code THEN
        RAISE EXCEPTION 'positions table is append-only - UPDATE on instrument_code column is forbidden'
            USING ERRCODE = 'P0001';
    END IF;
    IF OLD.bucket_key IS DISTINCT FROM NEW.bucket_key THEN
        RAISE EXCEPTION 'positions table is append-only - UPDATE on bucket_key column is forbidden'
            USING ERRCODE = 'P0001';
    END IF;
    IF OLD.reference_id IS DISTINCT FROM NEW.reference_id THEN
        RAISE EXCEPTION 'positions table is append-only - UPDATE on reference_id column is forbidden'
            USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER positions_append_only
    BEFORE UPDATE ON position_keeping.position
    FOR EACH ROW
    EXECUTE FUNCTION position_keeping.positions_append_only();

CREATE TABLE position_keeping.reservation (
    lien_id uuid NOT NULL,
    account_id character varying(255) NOT NULL,
    instrument_code character varying(32) NOT NULL,
    bucket_id character varying(256) NOT NULL DEFAULT '',
    reserved_amount decimal(38, 18) NOT NULL,
    status character varying(16) NOT NULL DEFAULT 'ACTIVE',
    created_at timestamptz NOT NULL DEFAULT now(),
    executed_at timestamptz NULL,
    terminated_at timestamptz NULL,
    PRIMARY KEY (lien_id),
    CONSTRAINT chk_reservation_status CHECK (status IN ('ACTIVE', 'EXECUTED', 'TERMINATED'))
);

CREATE INDEX idx_reservation_projected_balance
    ON position_keeping.reservation (account_id, instrument_code, status, bucket_id);
CREATE INDEX idx_reservation_active
    ON position_keeping.reservation (account_id, instrument_code, bucket_id)
    WHERE status = 'ACTIVE';
