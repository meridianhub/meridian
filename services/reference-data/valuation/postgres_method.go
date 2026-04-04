package valuation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// PostgresMethodRepository implements MethodRepository using PostgreSQL.
type PostgresMethodRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresMethodRepository creates a new PostgreSQL-backed method repository.
func NewPostgresMethodRepository(pool *pgxpool.Pool) *PostgresMethodRepository {
	return &PostgresMethodRepository{pool: pool}
}

func (r *PostgresMethodRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
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

func (r *PostgresMethodRepository) withReadTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
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
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit read transaction: %w", err)
	}
	return nil
}

func (r *PostgresMethodRepository) withWriteTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
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
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// Create creates a new valuation method in INITIATED status.
func (r *PostgresMethodRepository) Create(ctx context.Context, m *Method) error {
	if m.IsSystem {
		return ErrSystemReadOnly
	}

	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	now := time.Now()
	m.CreatedAt = now
	m.ValidFrom = now
	m.LifecycleStatus = StatusInitiated
	m.LogicHash = computeHash(m.LogicScript)

	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		query := `
			INSERT INTO valuation_method (
				id, name, version, input_instrument, output_instrument,
				logic_script, logic_hash, required_policies, lifecycle_status,
				is_system, description, created_at, valid_from
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8, $9,
				$10, $11, $12, $13
			)`

		_, err := tx.Exec(ctx, query,
			m.ID, m.Name, m.Version, m.InputInstrument, m.OutputInstrument,
			m.LogicScript, m.LogicHash, m.RequiredPolicies, string(m.LifecycleStatus),
			m.IsSystem, nullString(m.Description), m.CreatedAt, m.ValidFrom,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrAlreadyExists
			}
			return fmt.Errorf("failed to insert valuation method: %w", err)
		}
		return nil
	})
}

// GetByID retrieves a method by its UUID, optionally at a specific knowledge time.
func (r *PostgresMethodRepository) GetByID(ctx context.Context, id uuid.UUID, knowledgeAt *time.Time) (*Method, error) {
	var result *Method

	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		var query string
		var args []interface{}

		if knowledgeAt != nil {
			query = `
				SELECT id, name, version, input_instrument, output_instrument,
					logic_script, logic_hash, required_policies, lifecycle_status,
					is_system, description, created_at, activated_at, deprecated_at,
					valid_from, valid_to
				FROM valuation_method
				WHERE id = $1
					AND valid_from <= $2
					AND (valid_to IS NULL OR valid_to > $2)`
			args = []interface{}{id, *knowledgeAt}
		} else {
			query = `
				SELECT id, name, version, input_instrument, output_instrument,
					logic_script, logic_hash, required_policies, lifecycle_status,
					is_system, description, created_at, activated_at, deprecated_at,
					valid_from, valid_to
				FROM valuation_method
				WHERE id = $1`
			args = []interface{}{id}
		}

		row := tx.QueryRow(ctx, query, args...)
		m, err := scanMethod(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query valuation method: %w", err)
		}
		result = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Resolve finds the active method for given instruments.
// Resolution order: tenant override (is_system=false) first, then SYSTEM fallback.
func (r *PostgresMethodRepository) Resolve(ctx context.Context, inputInstrument, outputInstrument string) (*Method, error) {
	var result *Method

	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		// Step 1: tenant override
		tenantQuery := `
			SELECT id, name, version, input_instrument, output_instrument,
				logic_script, logic_hash, required_policies, lifecycle_status,
				is_system, description, created_at, activated_at, deprecated_at,
				valid_from, valid_to
			FROM valuation_method
			WHERE input_instrument = $1 AND output_instrument = $2
				AND lifecycle_status = 'ACTIVE' AND is_system = false
			ORDER BY version DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, tenantQuery, inputInstrument, outputInstrument)
		m, err := scanMethod(row)
		if err == nil {
			result = m
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("failed to query tenant valuation method: %w", err)
		}

		// Step 2: SYSTEM fallback
		systemQuery := `
			SELECT id, name, version, input_instrument, output_instrument,
				logic_script, logic_hash, required_policies, lifecycle_status,
				is_system, description, created_at, activated_at, deprecated_at,
				valid_from, valid_to
			FROM valuation_method
			WHERE input_instrument = $1 AND output_instrument = $2
				AND lifecycle_status = 'ACTIVE' AND is_system = true
			ORDER BY version DESC
			LIMIT 1`

		row = tx.QueryRow(ctx, systemQuery, inputInstrument, outputInstrument)
		m, err = scanMethod(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query system valuation method: %w", err)
		}
		result = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Activate transitions a method from INITIATED to ACTIVE.
func (r *PostgresMethodRepository) Activate(ctx context.Context, id uuid.UUID) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		var status string
		var isSystem bool
		var requiredPolicies []string

		err := tx.QueryRow(ctx,
			`SELECT lifecycle_status, is_system, required_policies FROM valuation_method WHERE id = $1`, id,
		).Scan(&status, &isSystem, &requiredPolicies)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check method: %w", err)
		}

		if isSystem {
			return ErrSystemReadOnly
		}
		if status != string(StatusInitiated) {
			return ErrNotInitiated
		}

		// Validate required policies exist and are active
		for _, policyName := range requiredPolicies {
			var exists bool
			err := tx.QueryRow(ctx,
				`SELECT EXISTS(
					SELECT 1 FROM valuation_policy
					WHERE name = $1 AND lifecycle_status = 'ACTIVE'
				)`, policyName,
			).Scan(&exists)
			if err != nil {
				return fmt.Errorf("failed to check required policy %s: %w", policyName, err)
			}
			if !exists {
				return fmt.Errorf("%w: %s", ErrRequiredPolicyMissing, policyName)
			}
		}

		now := time.Now()
		_, err = tx.Exec(ctx,
			`UPDATE valuation_method SET lifecycle_status = 'ACTIVE', activated_at = $1 WHERE id = $2`,
			now, id,
		)
		if err != nil {
			return fmt.Errorf("failed to activate method: %w", err)
		}
		return nil
	})
}

// Deprecate transitions a method from ACTIVE to DEPRECATED.
func (r *PostgresMethodRepository) Deprecate(ctx context.Context, id uuid.UUID) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		var status string
		var isSystem bool

		err := tx.QueryRow(ctx,
			`SELECT lifecycle_status, is_system FROM valuation_method WHERE id = $1`, id,
		).Scan(&status, &isSystem)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check method: %w", err)
		}

		if isSystem {
			return ErrSystemReadOnly
		}
		if status != string(StatusActive) {
			return ErrNotActive
		}

		now := time.Now()
		_, err = tx.Exec(ctx,
			`UPDATE valuation_method SET lifecycle_status = 'DEPRECATED', deprecated_at = $1, valid_to = $1 WHERE id = $2`,
			now, id,
		)
		if err != nil {
			return fmt.Errorf("failed to deprecate method: %w", err)
		}
		return nil
	})
}

func scanMethod(row pgx.Row) (*Method, error) {
	var m Method
	var status string
	var description sql.NullString

	err := row.Scan(
		&m.ID, &m.Name, &m.Version, &m.InputInstrument, &m.OutputInstrument,
		&m.LogicScript, &m.LogicHash, &m.RequiredPolicies, &status,
		&m.IsSystem, &description, &m.CreatedAt, &m.ActivatedAt, &m.DeprecatedAt,
		&m.ValidFrom, &m.ValidTo,
	)
	if err != nil {
		return nil, err
	}

	m.LifecycleStatus = LifecycleStatus(status)
	if description.Valid {
		m.Description = description.String
	}
	return &m, nil
}

func computeHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
