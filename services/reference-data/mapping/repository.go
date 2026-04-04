package mapping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Repository provides CRUD operations for MappingDefinition.
type Repository interface {
	Create(ctx context.Context, def *Definition) error
	GetByID(ctx context.Context, id uuid.UUID) (*Definition, error)
	GetLatestActive(ctx context.Context, name string) (*Definition, error)
	GetByNameAndVersion(ctx context.Context, name string, version int) (*Definition, error)
	ListByTenant(ctx context.Context, statusFilter Status, targetServiceFilter string, pageSize int, pageToken string) ([]*Definition, int, error)
	Update(ctx context.Context, def *Definition, expectedUpdatedAt time.Time) error
	UpdateStatus(ctx context.Context, id uuid.UUID, newStatus Status) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// sqlFetchStatus is a reusable query for checking the current status of a mapping by ID.
const sqlFetchStatus = `SELECT status FROM mapping_definition WHERE id = $1`

// PostgresRepository implements Repository using PostgreSQL via pgx.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgreSQL-backed mapping repository.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
func (r *PostgresRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.ErrMissingTenantContext
	}

	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// withReadTx executes fn within a read transaction with tenant scope.
func (r *PostgresRepository) withReadTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// withWriteTx executes fn within a write transaction with tenant scope.
func (r *PostgresRepository) withWriteTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Create inserts a new mapping definition in DRAFT status.
func (r *PostgresRepository) Create(ctx context.Context, def *Definition) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		if def.ID == uuid.Nil {
			def.ID = uuid.New()
		}
		now := time.Now().UTC()
		def.CreatedAt = now
		def.UpdatedAt = now
		def.Status = StatusDraft

		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			return tenant.ErrMissingTenantContext
		}
		def.TenantID = tenantID.String()

		fieldsJSON, err := MarshalFields(def.Fields)
		if err != nil {
			return fmt.Errorf("failed to marshal fields: %w", err)
		}
		inboundJSON, err := MarshalComputedFields(def.InboundComputed)
		if err != nil {
			return fmt.Errorf("failed to marshal inbound_computed_fields: %w", err)
		}
		outboundJSON, err := MarshalComputedFields(def.OutboundComputed)
		if err != nil {
			return fmt.Errorf("failed to marshal outbound_computed_fields: %w", err)
		}
		idempotencyJSON, err := MarshalIdempotency(def.Idempotency)
		if err != nil {
			return fmt.Errorf("failed to marshal idempotency: %w", err)
		}

		query := `
			INSERT INTO mapping_definition (
				id, tenant_id, name, target_service, target_rpc, version, status,
				external_schema, fields, inbound_computed_fields, outbound_computed_fields,
				inbound_validation_cel, outbound_validation_cel,
				is_batch, batch_target_path, idempotency,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7,
				$8, $9, $10, $11,
				$12, $13,
				$14, $15, $16,
				$17, $18
			)`

		_, err = tx.Exec(ctx, query,
			def.ID, def.TenantID, def.Name, def.TargetService, def.TargetRPC, def.Version, string(def.Status),
			nullString(def.ExternalSchema), fieldsJSON, inboundJSON, outboundJSON,
			nullString(def.InboundValidationCEL), nullString(def.OutboundValidationCEL),
			def.IsBatch, nullString(def.BatchTargetPath), idempotencyJSON,
			def.CreatedAt, def.UpdatedAt,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrAlreadyExists
			}
			return fmt.Errorf("failed to insert mapping definition: %w", err)
		}
		return nil
	})
}

// GetByID retrieves a mapping definition by its UUID.
func (r *PostgresRepository) GetByID(ctx context.Context, id uuid.UUID) (*Definition, error) {
	var result *Definition
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		def, err := r.getByIDInTx(ctx, tx, id)
		if err != nil {
			return err
		}
		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *PostgresRepository) getByIDInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Definition, error) {
	query := `
		SELECT id, tenant_id, name, target_service, target_rpc, version, status,
			external_schema, fields, inbound_computed_fields, outbound_computed_fields,
			inbound_validation_cel, outbound_validation_cel,
			is_batch, batch_target_path, idempotency,
			created_at, updated_at
		FROM mapping_definition
		WHERE id = $1`
	row := tx.QueryRow(ctx, query, id)
	return r.scanRow(row)
}

// GetLatestActive retrieves the latest ACTIVE mapping definition by name.
func (r *PostgresRepository) GetLatestActive(ctx context.Context, name string) (*Definition, error) {
	var result *Definition
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, tenant_id, name, target_service, target_rpc, version, status,
				external_schema, fields, inbound_computed_fields, outbound_computed_fields,
				inbound_validation_cel, outbound_validation_cel,
				is_batch, batch_target_path, idempotency,
				created_at, updated_at
			FROM mapping_definition
			WHERE name = $1 AND status = 'ACTIVE'
			ORDER BY version DESC
			LIMIT 1`
		row := tx.QueryRow(ctx, query, name)
		def, err := r.scanRow(row)
		if err != nil {
			return err
		}
		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// GetByNameAndVersion retrieves a specific mapping definition by name and version.
func (r *PostgresRepository) GetByNameAndVersion(ctx context.Context, name string, version int) (*Definition, error) {
	var result *Definition
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, tenant_id, name, target_service, target_rpc, version, status,
				external_schema, fields, inbound_computed_fields, outbound_computed_fields,
				inbound_validation_cel, outbound_validation_cel,
				is_batch, batch_target_path, idempotency,
				created_at, updated_at
			FROM mapping_definition
			WHERE name = $1 AND version = $2
			LIMIT 1`
		row := tx.QueryRow(ctx, query, name, version)
		def, err := r.scanRow(row)
		if err != nil {
			return err
		}
		result = def
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ListByTenant returns mapping definitions for the current tenant with optional filtering.
// Returns (results, totalCount, error).
func (r *PostgresRepository) ListByTenant(ctx context.Context, statusFilter Status, targetServiceFilter string, pageSize int, pageToken string) ([]*Definition, int, error) {
	var results []*Definition
	var total int

	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		args := []any{}
		whereClause := ""
		argIdx := 1

		if statusFilter != "" {
			whereClause += fmt.Sprintf(" AND status = $%d", argIdx)
			args = append(args, string(statusFilter))
			argIdx++
		}
		if targetServiceFilter != "" {
			whereClause += fmt.Sprintf(" AND target_service = $%d", argIdx)
			args = append(args, targetServiceFilter)
			argIdx++
		}

		// Count query
		countQuery := fmt.Sprintf("SELECT COUNT(*) FROM mapping_definition WHERE 1=1%s", whereClause)
		if err := tx.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
			return fmt.Errorf("failed to count mappings: %w", err)
		}

		// Cursor pagination: pageToken is the last id seen
		if pageToken != "" {
			whereClause += fmt.Sprintf(" AND id > $%d", argIdx)
			args = append(args, pageToken)
			argIdx++
		}

		if pageSize <= 0 {
			pageSize = 20
		}
		args = append(args, pageSize)

		listQuery := fmt.Sprintf(`
			SELECT id, tenant_id, name, target_service, target_rpc, version, status,
				external_schema, fields, inbound_computed_fields, outbound_computed_fields,
				inbound_validation_cel, outbound_validation_cel,
				is_batch, batch_target_path, idempotency,
				created_at, updated_at
			FROM mapping_definition
			WHERE 1=1%s
			ORDER BY id
			LIMIT $%d`, whereClause, argIdx)

		rows, err := tx.Query(ctx, listQuery, args...)
		if err != nil {
			return fmt.Errorf("failed to list mappings: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			def, err := r.scanRows(rows)
			if err != nil {
				return err
			}
			results = append(results, def)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// Update applies field updates to an existing DRAFT mapping definition using optimistic locking.
func (r *PostgresRepository) Update(ctx context.Context, def *Definition, expectedUpdatedAt time.Time) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		// Verify it exists and is DRAFT
		var currentStatus string
		checkQuery := sqlFetchStatus
		if err := tx.QueryRow(ctx, checkQuery, def.ID).Scan(&currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to fetch mapping for update: %w", err)
		}
		if currentStatus != string(StatusDraft) {
			return ErrNotDraft
		}

		fieldsJSON, err := MarshalFields(def.Fields)
		if err != nil {
			return fmt.Errorf("failed to marshal fields: %w", err)
		}
		inboundJSON, err := MarshalComputedFields(def.InboundComputed)
		if err != nil {
			return fmt.Errorf("failed to marshal inbound_computed_fields: %w", err)
		}
		outboundJSON, err := MarshalComputedFields(def.OutboundComputed)
		if err != nil {
			return fmt.Errorf("failed to marshal outbound_computed_fields: %w", err)
		}
		idempotencyJSON, err := MarshalIdempotency(def.Idempotency)
		if err != nil {
			return fmt.Errorf("failed to marshal idempotency: %w", err)
		}

		now := time.Now().UTC()
		updateQuery := `
			UPDATE mapping_definition SET
				name = $1,
				external_schema = $2,
				fields = $3,
				inbound_computed_fields = $4,
				outbound_computed_fields = $5,
				inbound_validation_cel = $6,
				outbound_validation_cel = $7,
				is_batch = $8,
				batch_target_path = $9,
				idempotency = $10,
				updated_at = $11
			WHERE id = $12 AND updated_at = $13`

		result, err := tx.Exec(ctx, updateQuery,
			def.Name,
			nullString(def.ExternalSchema),
			fieldsJSON, inboundJSON, outboundJSON,
			nullString(def.InboundValidationCEL), nullString(def.OutboundValidationCEL),
			def.IsBatch, nullString(def.BatchTargetPath), idempotencyJSON,
			now,
			def.ID, expectedUpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to update mapping definition: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrOptimisticLock
		}
		def.UpdatedAt = now
		return nil
	})
}

// UpdateStatus transitions the status of a mapping definition.
// Enforces lifecycle: DRAFT → ACTIVE → DEPRECATED.
func (r *PostgresRepository) UpdateStatus(ctx context.Context, id uuid.UUID, newStatus Status) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		var currentStatus string
		checkQuery := sqlFetchStatus
		if err := tx.QueryRow(ctx, checkQuery, id).Scan(&currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to fetch mapping status: %w", err)
		}

		current := Status(currentStatus)
		if !current.CanTransitionTo(newStatus) {
			return fmt.Errorf("%w: %s → %s", ErrInvalidStatusTransition, current, newStatus)
		}

		now := time.Now().UTC()
		updateQuery := `UPDATE mapping_definition SET status = $1, updated_at = $2 WHERE id = $3`
		result, err := tx.Exec(ctx, updateQuery, string(newStatus), now, id)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrAlreadyExists
			}
			return fmt.Errorf("failed to update mapping status: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Delete removes a DRAFT or DEPRECATED mapping definition. Returns ErrNotActive if ACTIVE.
// Uses SELECT FOR UPDATE to prevent a concurrent activation racing the delete.
func (r *PostgresRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		var currentStatus string
		lockQuery := `SELECT status FROM mapping_definition WHERE id = $1 FOR UPDATE`
		if err := tx.QueryRow(ctx, lockQuery, id).Scan(&currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to fetch mapping for delete: %w", err)
		}

		if currentStatus == string(StatusActive) {
			return ErrNotActive
		}

		deleteQuery := `DELETE FROM mapping_definition WHERE id = $1`
		_, err := tx.Exec(ctx, deleteQuery, id)
		if err != nil {
			return fmt.Errorf("failed to delete mapping definition: %w", err)
		}
		return nil
	})
}

// --- Scan helpers ---

func (r *PostgresRepository) scanRow(row pgx.Row) (*Definition, error) {
	var def Definition
	var status string
	var externalSchema, inboundCEL, outboundCEL, batchTargetPath sql.NullString
	var fieldsJSON, inboundComputedJSON, outboundComputedJSON, idempotencyJSON []byte

	err := row.Scan(
		&def.ID, &def.TenantID, &def.Name, &def.TargetService, &def.TargetRPC,
		&def.Version, &status,
		&externalSchema, &fieldsJSON, &inboundComputedJSON, &outboundComputedJSON,
		&inboundCEL, &outboundCEL,
		&def.IsBatch, &batchTargetPath, &idempotencyJSON,
		&def.CreatedAt, &def.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to scan mapping definition: %w", err)
	}

	return populateFromScan(&def, status, externalSchema, inboundCEL, outboundCEL, batchTargetPath,
		fieldsJSON, inboundComputedJSON, outboundComputedJSON, idempotencyJSON)
}

func (r *PostgresRepository) scanRows(rows pgx.Rows) (*Definition, error) {
	var def Definition
	var status string
	var externalSchema, inboundCEL, outboundCEL, batchTargetPath sql.NullString
	var fieldsJSON, inboundComputedJSON, outboundComputedJSON, idempotencyJSON []byte

	err := rows.Scan(
		&def.ID, &def.TenantID, &def.Name, &def.TargetService, &def.TargetRPC,
		&def.Version, &status,
		&externalSchema, &fieldsJSON, &inboundComputedJSON, &outboundComputedJSON,
		&inboundCEL, &outboundCEL,
		&def.IsBatch, &batchTargetPath, &idempotencyJSON,
		&def.CreatedAt, &def.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan mapping definition row: %w", err)
	}

	return populateFromScan(&def, status, externalSchema, inboundCEL, outboundCEL, batchTargetPath,
		fieldsJSON, inboundComputedJSON, outboundComputedJSON, idempotencyJSON)
}

func populateFromScan(
	def *Definition,
	status string,
	externalSchema, inboundCEL, outboundCEL, batchTargetPath sql.NullString,
	fieldsJSON, inboundComputedJSON, outboundComputedJSON, idempotencyJSON []byte,
) (*Definition, error) {
	def.Status = Status(status)
	if externalSchema.Valid {
		def.ExternalSchema = externalSchema.String
	}
	if inboundCEL.Valid {
		def.InboundValidationCEL = inboundCEL.String
	}
	if outboundCEL.Valid {
		def.OutboundValidationCEL = outboundCEL.String
	}
	if batchTargetPath.Valid {
		def.BatchTargetPath = batchTargetPath.String
	}

	fields, err := UnmarshalFields(fieldsJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal fields: %w", err)
	}
	def.Fields = fields

	inbound, err := UnmarshalComputedFields(inboundComputedJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal inbound_computed_fields: %w", err)
	}
	def.InboundComputed = inbound

	outbound, err := UnmarshalComputedFields(outboundComputedJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal outbound_computed_fields: %w", err)
	}
	def.OutboundComputed = outbound

	idempotency, err := UnmarshalIdempotency(idempotencyJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal idempotency: %w", err)
	}
	def.Idempotency = idempotency

	return def, nil
}

// nullString converts a string to sql.NullString, treating empty strings as NULL.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
