package valuation

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
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PostgresPolicyRepository implements PolicyRepository using PostgreSQL.
type PostgresPolicyRepository struct {
	pool     *pgxpool.Pool
	compiler *refcel.Compiler
}

// NewPostgresPolicyRepository creates a new PostgreSQL-backed policy repository.
func NewPostgresPolicyRepository(pool *pgxpool.Pool, compiler *refcel.Compiler) *PostgresPolicyRepository {
	return &PostgresPolicyRepository{
		pool:     pool,
		compiler: compiler,
	}
}

func (r *PostgresPolicyRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
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

func (r *PostgresPolicyRepository) withReadTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
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

func (r *PostgresPolicyRepository) withWriteTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
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

// Create creates a new valuation policy in INITIATED status.
func (r *PostgresPolicyRepository) Create(ctx context.Context, p *Policy) error {
	if p.IsSystem {
		return ErrSystemReadOnly
	}

	// Validate CEL expression at creation time (fail-fast).
	// Valuation policies return numeric values, so use CompileValueExpression
	// rather than CompileValidation (which enforces boolean return type).
	_, err := r.compiler.CompileValueExpression(p.CelExpression)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidCEL, err)
	}

	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	now := time.Now()
	p.CreatedAt = now
	p.ValidFrom = now
	p.LifecycleStatus = StatusInitiated
	p.CelHash = computeHash(p.CelExpression)

	// Estimate cost if not set
	if p.EstimatedCost <= 0 {
		p.EstimatedCost = estimateCELCost(p.CelExpression)
	}

	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		query := `
			INSERT INTO valuation_policy (
				id, name, version, cel_expression, cel_hash,
				input_schema, output_type, estimated_cost, lifecycle_status,
				is_system, description, created_at, valid_from
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8, $9,
				$10, $11, $12, $13
			)`

		_, err := tx.Exec(ctx, query,
			p.ID, p.Name, p.Version, p.CelExpression, p.CelHash,
			p.InputSchema, p.OutputType, p.EstimatedCost, string(p.LifecycleStatus),
			p.IsSystem, nullString(p.Description), p.CreatedAt, p.ValidFrom,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrAlreadyExists
			}
			return fmt.Errorf("failed to insert valuation policy: %w", err)
		}
		return nil
	})
}

// GetByName retrieves a policy by name, optionally at a specific knowledge time.
func (r *PostgresPolicyRepository) GetByName(ctx context.Context, name string, knowledgeAt *time.Time) (*Policy, error) {
	var result *Policy

	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		var query string
		var args []interface{}

		if knowledgeAt != nil {
			query = `
				SELECT id, name, version, cel_expression, cel_hash,
					input_schema, output_type, estimated_cost, lifecycle_status,
					is_system, description, created_at, activated_at, deprecated_at,
					valid_from, valid_to
				FROM valuation_policy
				WHERE name = $1
					AND valid_from <= $2
					AND (valid_to IS NULL OR valid_to > $2)
				ORDER BY version DESC
				LIMIT 1`
			args = []interface{}{name, *knowledgeAt}
		} else {
			query = `
				SELECT id, name, version, cel_expression, cel_hash,
					input_schema, output_type, estimated_cost, lifecycle_status,
					is_system, description, created_at, activated_at, deprecated_at,
					valid_from, valid_to
				FROM valuation_policy
				WHERE name = $1
				ORDER BY version DESC
				LIMIT 1`
			args = []interface{}{name}
		}

		row := tx.QueryRow(ctx, query, args...)
		p, err := scanPolicy(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query valuation policy: %w", err)
		}
		result = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Resolve finds the active policy by name, checking tenant first then SYSTEM.
func (r *PostgresPolicyRepository) Resolve(ctx context.Context, name string) (*Policy, error) {
	var result *Policy

	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		// Step 1: tenant override
		tenantQuery := `
			SELECT id, name, version, cel_expression, cel_hash,
				input_schema, output_type, estimated_cost, lifecycle_status,
				is_system, description, created_at, activated_at, deprecated_at,
				valid_from, valid_to
			FROM valuation_policy
			WHERE name = $1 AND lifecycle_status = 'ACTIVE' AND is_system = false
			ORDER BY version DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, tenantQuery, name)
		p, err := scanPolicy(row)
		if err == nil {
			result = p
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("failed to query tenant valuation policy: %w", err)
		}

		// Step 2: SYSTEM fallback
		systemQuery := `
			SELECT id, name, version, cel_expression, cel_hash,
				input_schema, output_type, estimated_cost, lifecycle_status,
				is_system, description, created_at, activated_at, deprecated_at,
				valid_from, valid_to
			FROM valuation_policy
			WHERE name = $1 AND lifecycle_status = 'ACTIVE' AND is_system = true
			ORDER BY version DESC
			LIMIT 1`

		row = tx.QueryRow(ctx, systemQuery, name)
		p, err = scanPolicy(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query system valuation policy: %w", err)
		}
		result = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Activate transitions a policy from INITIATED to ACTIVE.
func (r *PostgresPolicyRepository) Activate(ctx context.Context, id uuid.UUID) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		var status string
		var isSystem bool

		err := tx.QueryRow(ctx,
			`SELECT lifecycle_status, is_system FROM valuation_policy WHERE id = $1`, id,
		).Scan(&status, &isSystem)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check policy: %w", err)
		}

		if isSystem {
			return ErrSystemReadOnly
		}
		if status != string(StatusInitiated) {
			return ErrNotInitiated
		}

		now := time.Now()
		_, err = tx.Exec(ctx,
			`UPDATE valuation_policy SET lifecycle_status = 'ACTIVE', activated_at = $1 WHERE id = $2`,
			now, id,
		)
		if err != nil {
			return fmt.Errorf("failed to activate policy: %w", err)
		}
		return nil
	})
}

// Deprecate transitions a policy from ACTIVE to DEPRECATED.
func (r *PostgresPolicyRepository) Deprecate(ctx context.Context, id uuid.UUID) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		var status string
		var isSystem bool

		err := tx.QueryRow(ctx,
			`SELECT lifecycle_status, is_system FROM valuation_policy WHERE id = $1`, id,
		).Scan(&status, &isSystem)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check policy: %w", err)
		}

		if isSystem {
			return ErrSystemReadOnly
		}
		if status != string(StatusActive) {
			return ErrNotActive
		}

		now := time.Now()
		_, err = tx.Exec(ctx,
			`UPDATE valuation_policy SET lifecycle_status = 'DEPRECATED', deprecated_at = $1, valid_to = $1 WHERE id = $2`,
			now, id,
		)
		if err != nil {
			return fmt.Errorf("failed to deprecate policy: %w", err)
		}
		return nil
	})
}

// DryRun evaluates a policy's CEL expression with sample inputs.
func (r *PostgresPolicyRepository) DryRun(ctx context.Context, policyName string, sampleInputs map[string]string) (*DryRunResult, error) {
	policy, err := r.Resolve(ctx, policyName)
	if err != nil {
		return nil, err
	}

	prg, compileErr := r.compiler.CompileValueExpression(policy.CelExpression)
	if compileErr != nil {
		return failedDryRun(policy.EstimatedCost, compileErr.Error()), nil
	}

	// Build input for CEL evaluation
	input := buildDryRunInput(sampleInputs)

	out, _, evalErr := prg.Eval(input)
	if evalErr != nil {
		return failedDryRun(policy.EstimatedCost, evalErr.Error()), nil
	}

	return &DryRunResult{
		Success:       true,
		Output:        fmt.Sprintf("%v", out.Value()),
		EstimatedCost: policy.EstimatedCost,
	}, nil
}

func buildDryRunInput(sampleInputs map[string]string) map[string]any {
	attrs := sampleInputs
	if attrs == nil {
		attrs = make(map[string]string)
	}

	amount := ""
	if v, ok := attrs["amount"]; ok {
		amount = v
	}

	return map[string]any{
		"attributes": attrs,
		"amount":     amount,
		"source":     "",
		"valid_from": time.Time{},
		"valid_to":   time.Time{},
	}
}

func scanPolicy(row pgx.Row) (*Policy, error) {
	var p Policy
	var status string
	var description, outputType sql.NullString

	err := row.Scan(
		&p.ID, &p.Name, &p.Version, &p.CelExpression, &p.CelHash,
		&p.InputSchema, &outputType, &p.EstimatedCost, &status,
		&p.IsSystem, &description, &p.CreatedAt, &p.ActivatedAt, &p.DeprecatedAt,
		&p.ValidFrom, &p.ValidTo,
	)
	if err != nil {
		return nil, err
	}

	p.LifecycleStatus = LifecycleStatus(status)
	if description.Valid {
		p.Description = description.String
	}
	if outputType.Valid {
		p.OutputType = outputType.String
	}
	return &p, nil
}

func failedDryRun(estimatedCost int, errMsg string) *DryRunResult {
	return &DryRunResult{
		Success:       false,
		EstimatedCost: estimatedCost,
		Errors:        []string{errMsg},
	}
}

// estimateCELCost provides a heuristic cost estimation based on expression length.
func estimateCELCost(expr string) int {
	cost := len(expr) / 10
	if cost < 1 {
		cost = 1
	}
	if cost > 9999 {
		cost = 9999
	}
	return cost
}
