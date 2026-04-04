package accounttype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PostgresRegistry implements Registry using PostgreSQL via pgx.
type PostgresRegistry struct {
	pool     *pgxpool.Pool
	compiler *refcel.Compiler

	programCache   map[string]cel.Program
	programCacheMu sync.RWMutex
}

// NewPostgresRegistry creates a new PostgreSQL-backed account type registry.
func NewPostgresRegistry(pool *pgxpool.Pool) (*PostgresRegistry, error) {
	compiler, err := refcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL compiler: %w", err)
	}

	return &PostgresRegistry{
		pool:         pool,
		compiler:     compiler,
		programCache: make(map[string]cel.Program),
	}, nil
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
func (r *PostgresRegistry) setSearchPath(ctx context.Context, tx pgx.Tx) error {
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

// withReadTransaction executes a read-only function within a transaction with tenant scoping.
func (r *PostgresRegistry) withReadTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

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

// withWriteTransaction executes a write function within a transaction with tenant scoping.
func (r *PostgresRegistry) withWriteTransaction(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

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

// CreateDraft creates a new account type definition in DRAFT status.
// Uses INSERT ... ON CONFLICT (code, version) DO NOTHING.
// Returns the existing definition if conflict (idempotent).
func (r *PostgresRegistry) CreateDraft(ctx context.Context, def *Definition) error {
	if err := compileCELFields(r.compiler, def.ValidationCEL, def.BucketingCEL, def.EligibilityCEL); err != nil {
		return err
	}

	if err := validateSchemaIfPresent(def.AttributeSchema); err != nil {
		return err
	}

	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		return r.insertDraft(ctx, tx, def)
	})
}

func (r *PostgresRegistry) insertDraft(ctx context.Context, tx pgx.Tx, def *Definition) error {
	if def.ID == uuid.Nil {
		def.ID = uuid.New()
	}

	now := time.Now().UTC()
	def.CreatedAt = now
	def.UpdatedAt = now
	def.Status = StatusDraft

	attrsJSON, err := marshalAttributes(def.Attributes)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO account_type_definitions (
			id, code, version, display_name, description,
			normal_balance, behavior_class, instrument_code,
			default_saga_prefix, default_conversion_method_id, default_conversion_method_version,
			validation_cel, bucketing_cel, eligibility_cel,
			attribute_schema, attributes,
			status, is_system,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11,
			$12, $13, $14,
			$15, $16,
			$17, $18,
			$19, $20
		)
		ON CONFLICT (code, version) DO NOTHING`

	result, err := tx.Exec(ctx, query,
		def.ID, def.Code, def.Version, def.DisplayName, nullString(def.Description),
		string(def.NormalBalance), string(def.BehaviorClass), def.InstrumentCode,
		nullString(def.DefaultSagaPrefix), def.DefaultConversionMethodID, def.DefaultConversionMethodVersion,
		nullString(def.ValidationCEL), nullString(def.BucketingCEL), nullString(def.EligibilityCEL),
		def.AttributeSchema, attrsJSON,
		string(def.Status), def.IsSystem,
		def.CreatedAt, def.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert account type definition: %w", err)
	}

	if result.RowsAffected() == 0 {
		existing, err := r.getDefinitionInTx(ctx, tx, def.Code, def.Version)
		if err != nil {
			return fmt.Errorf("failed to load existing definition after conflict: %w", err)
		}
		*def = *existing
		return nil
	}

	return r.insertValuationMethodTemplates(ctx, tx, def, now)
}

func (r *PostgresRegistry) insertValuationMethodTemplates(ctx context.Context, tx pgx.Tx, def *Definition, now time.Time) error {
	for i := range def.ValuationMethods {
		vmt := &def.ValuationMethods[i]
		if vmt.ID == uuid.Nil {
			vmt.ID = uuid.New()
		}
		vmt.AccountTypeID = def.ID
		vmt.Status = StatusDraft
		vmt.CreatedAt = now
		vmt.UpdatedAt = now

		vmtParams, err := marshalAttributes(vmt.Parameters)
		if err != nil {
			return fmt.Errorf("failed to marshal valuation method parameters: %w", err)
		}

		vmtQuery := `
			INSERT INTO account_type_valuation_methods (
				id, account_type_id, input_instrument,
				valuation_method_id, valuation_method_version,
				parameters, status, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT DO NOTHING`

		if _, err = tx.Exec(ctx, vmtQuery,
			vmt.ID, vmt.AccountTypeID, vmt.InputInstrument,
			vmt.ValuationMethodID, vmt.ValuationMethodVersion,
			vmtParams, string(vmt.Status), vmt.CreatedAt, vmt.UpdatedAt,
		); err != nil {
			return fmt.Errorf("failed to insert valuation method template: %w", err)
		}
	}
	return nil
}

// updateCurrent holds the current state of a definition fetched for update.
type updateCurrent struct {
	code          string
	status        string
	isSystem      bool
	behaviorClass string
	updatedAt     time.Time
}

// UpdateDefinition updates a DRAFT account type definition.
func (r *PostgresRegistry) UpdateDefinition(ctx context.Context, code string, version int, updates *Definition) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		current, err := r.fetchForUpdate(ctx, tx, code, version)
		if err != nil {
			return err
		}

		if err := checkImmutableFields(updates, current); err != nil {
			return err
		}

		if err := compileCELFields(r.compiler, updates.ValidationCEL, updates.BucketingCEL, updates.EligibilityCEL); err != nil {
			return err
		}

		return r.applyUpdate(ctx, tx, code, version, updates, current.updatedAt)
	})
}

func (r *PostgresRegistry) fetchForUpdate(ctx context.Context, tx pgx.Tx, code string, version int) (*updateCurrent, error) {
	var cur updateCurrent
	checkQuery := `SELECT code, status, is_system, behavior_class, updated_at
		FROM account_type_definitions WHERE code = $1 AND version = $2`
	err := tx.QueryRow(ctx, checkQuery, code, version).Scan(
		&cur.code, &cur.status, &cur.isSystem, &cur.behaviorClass, &cur.updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to check account type: %w", err)
	}
	if cur.isSystem {
		return nil, ErrSystemAccountTypeReadOnly
	}
	if cur.status != string(StatusDraft) {
		return nil, ErrNotDraft
	}
	return &cur, nil
}

func checkImmutableFields(updates *Definition, current *updateCurrent) error {
	if updates.Code != "" && updates.Code != current.code {
		return fmt.Errorf("%w: Code", ErrFieldImmutable)
	}
	if updates.IsSystem != current.isSystem && updates.IsSystem {
		return fmt.Errorf("%w: IsSystem", ErrFieldImmutable)
	}
	if updates.BehaviorClass != "" && string(updates.BehaviorClass) != current.behaviorClass {
		return fmt.Errorf("%w: BehaviorClass", ErrFieldImmutable)
	}
	return nil
}

func (r *PostgresRegistry) applyUpdate(ctx context.Context, tx pgx.Tx, code string, version int, updates *Definition, expectedUpdatedAt time.Time) error {
	now := time.Now().UTC()

	var attrsJSON []byte
	if updates.Attributes != nil {
		var err error
		attrsJSON, err = json.Marshal(updates.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}
	}

	updateQuery := `
		UPDATE account_type_definitions SET
			display_name = COALESCE(NULLIF($1, ''), display_name),
			description = COALESCE($2, description),
			normal_balance = COALESCE(NULLIF($3, ''), normal_balance),
			instrument_code = COALESCE(NULLIF($4, ''), instrument_code),
			default_saga_prefix = COALESCE($5, default_saga_prefix),
			default_conversion_method_id = COALESCE($6, default_conversion_method_id),
			default_conversion_method_version = COALESCE($7, default_conversion_method_version),
			validation_cel = COALESCE($8, validation_cel),
			bucketing_cel = COALESCE($9, bucketing_cel),
			eligibility_cel = COALESCE($10, eligibility_cel),
			attribute_schema = COALESCE($11, attribute_schema),
			attributes = COALESCE($12, attributes),
			updated_at = $13
		WHERE code = $14 AND version = $15 AND updated_at = $16`

	result, err := tx.Exec(ctx, updateQuery,
		updates.DisplayName,
		nullString(updates.Description),
		string(updates.NormalBalance),
		updates.InstrumentCode,
		nullString(updates.DefaultSagaPrefix),
		updates.DefaultConversionMethodID,
		updates.DefaultConversionMethodVersion,
		nullString(updates.ValidationCEL),
		nullString(updates.BucketingCEL),
		nullString(updates.EligibilityCEL),
		updates.AttributeSchema,
		attrsJSON,
		now,
		code, version, expectedUpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update account type definition: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrOptimisticLock
	}

	r.invalidateCache(code, version)
	return nil
}

// ActivateAccountType transitions a definition from DRAFT to ACTIVE.
func (r *PostgresRegistry) ActivateAccountType(ctx context.Context, code string, version int) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		def, err := r.getDefinitionInTx(ctx, tx, code, version)
		if err != nil {
			return err
		}

		if def.Status == StatusActive {
			return nil // idempotent
		}
		if !def.Status.CanTransitionTo(StatusActive) {
			return ErrNotDraft
		}

		methods, err := r.loadValuationMethods(ctx, tx, def.ID)
		if err != nil {
			return err
		}
		def.ValuationMethods = methods

		if errs := r.runActivationPreChecks(ctx, tx, def); len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			return fmt.Errorf("activation pre-check failed: %s: %w",
				strings.Join(msgs, "; "), errors.Join(errs...))
		}

		return r.setActive(ctx, tx, code, version, def.ID)
	})
}

func (r *PostgresRegistry) runActivationPreChecks(ctx context.Context, tx pgx.Tx, def *Definition) []error {
	var errs []error

	if err := r.checkInstrumentActive(ctx, tx, def.InstrumentCode); err != nil {
		errs = append(errs, fmt.Errorf("instrument: %w", err))
	}

	if def.DefaultConversionMethodID != nil {
		if err := r.checkValuationMethodExists(ctx, tx, *def.DefaultConversionMethodID, *def.DefaultConversionMethodVersion); err != nil {
			errs = append(errs, fmt.Errorf("default conversion method: %w", err))
		}
	}

	errs = append(errs, r.checkValuationMethodTemplates(ctx, tx, def.ValuationMethods)...)

	if err := compileCELFields(r.compiler, def.ValidationCEL, def.BucketingCEL, def.EligibilityCEL); err != nil {
		errs = append(errs, fmt.Errorf("CEL: %w", err))
	}

	errs = append(errs, r.checkSchemaAndAttributes(def)...)

	if def.DefaultSagaPrefix != "" {
		if err := r.checkSagaExists(ctx, tx, def.DefaultSagaPrefix); err != nil {
			errs = append(errs, fmt.Errorf("saga prefix: %w", err))
		}
	}

	if err := r.checkNoActiveCodeDuplicate(ctx, tx, def.Code, def.ID); err != nil {
		errs = append(errs, err)
	}

	return errs
}

func (r *PostgresRegistry) checkValuationMethodTemplates(ctx context.Context, tx pgx.Tx, methods []ValuationMethodTemplate) []error {
	var errs []error
	for _, vmt := range methods {
		if err := r.checkValuationMethodExists(ctx, tx, vmt.ValuationMethodID, vmt.ValuationMethodVersion); err != nil {
			errs = append(errs, fmt.Errorf("valuation method template %s: method: %w", vmt.InputInstrument, err))
		}
		if err := r.checkInstrumentActive(ctx, tx, vmt.InputInstrument); err != nil {
			errs = append(errs, fmt.Errorf("valuation method template %s: instrument: %w", vmt.InputInstrument, err))
		}
	}
	return errs
}

func (r *PostgresRegistry) checkSchemaAndAttributes(def *Definition) []error {
	var errs []error
	if !hasNonEmptySchema(def.AttributeSchema) {
		return errs
	}

	if err := validateJSONSchema(def.AttributeSchema); err != nil {
		errs = append(errs, fmt.Errorf("attribute schema: %w", err))
	} else if def.Attributes != nil {
		if err := validateAttributesAgainstSchema(def.AttributeSchema, def.Attributes); err != nil {
			errs = append(errs, fmt.Errorf("attributes: %w", err))
		}
	}
	return errs
}

func (r *PostgresRegistry) checkNoActiveCodeDuplicate(ctx context.Context, tx pgx.Tx, code string, defID uuid.UUID) error {
	var activeCount int
	countQuery := `SELECT COUNT(*) FROM account_type_definitions
		WHERE code = $1 AND status = 'ACTIVE' AND id != $2`
	if err := tx.QueryRow(ctx, countQuery, code, defID).Scan(&activeCount); err != nil {
		return fmt.Errorf("failed to check active code: %w", err)
	}
	if activeCount > 0 {
		return ErrActiveCodeExists
	}
	return nil
}

func (r *PostgresRegistry) setActive(ctx context.Context, tx pgx.Tx, code string, version int, defID uuid.UUID) error {
	now := time.Now().UTC()
	updateQuery := `
		UPDATE account_type_definitions SET
			status = 'ACTIVE',
			activated_at = $1,
			updated_at = $1
		WHERE code = $2 AND version = $3`

	_, err := tx.Exec(ctx, updateQuery, now, code, version)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrActiveCodeExists
		}
		return fmt.Errorf("failed to activate account type: %w", err)
	}

	vmtUpdateQuery := `
		UPDATE account_type_valuation_methods SET
			status = 'ACTIVE',
			updated_at = $1
		WHERE account_type_id = $2 AND status = 'DRAFT'`
	_, err = tx.Exec(ctx, vmtUpdateQuery, now, defID)
	if err != nil {
		return fmt.Errorf("failed to activate valuation method templates: %w", err)
	}

	return nil
}

// DeprecateAccountType transitions a definition from ACTIVE to DEPRECATED.
func (r *PostgresRegistry) DeprecateAccountType(ctx context.Context, code string, version int, successorID *uuid.UUID) error {
	return r.withWriteTransaction(ctx, func(tx pgx.Tx) error {
		var defID uuid.UUID
		var currentStatus string
		var isSystem bool
		var existingSuccessorID *uuid.UUID

		checkQuery := `SELECT id, status, is_system, successor_id
			FROM account_type_definitions WHERE code = $1 AND version = $2`
		err := tx.QueryRow(ctx, checkQuery, code, version).Scan(
			&defID, &currentStatus, &isSystem, &existingSuccessorID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to check account type: %w", err)
		}

		if isSystem {
			return ErrSystemAccountTypeReadOnly
		}

		if currentStatus != string(StatusActive) {
			return ErrNotActive
		}

		// Enforce write-once semantics for successor_id (no change/clear once set)
		if existingSuccessorID != nil {
			if successorID == nil || *existingSuccessorID != *successorID {
				return ErrSuccessorWriteOnce
			}
		}

		now := time.Now().UTC()
		updateQuery := `
			UPDATE account_type_definitions SET
				status = 'DEPRECATED',
				deprecated_at = $1,
				updated_at = $1,
				successor_id = $4
			WHERE code = $2 AND version = $3`

		_, err = tx.Exec(ctx, updateQuery, now, code, version, successorID)
		if err != nil {
			return fmt.Errorf("failed to deprecate account type: %w", err)
		}

		return nil
	})
}

func (r *PostgresRegistry) checkInstrumentActive(ctx context.Context, tx pgx.Tx, instrumentCode string) error {
	var count int
	query := `SELECT COUNT(*) FROM instrument_definition WHERE code = $1 AND status = 'ACTIVE'`
	if err := tx.QueryRow(ctx, query, instrumentCode).Scan(&count); err != nil {
		return fmt.Errorf("failed to check instrument: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: %s", ErrInvalidInstrument, instrumentCode)
	}
	return nil
}

func (r *PostgresRegistry) checkValuationMethodExists(ctx context.Context, tx pgx.Tx, methodID uuid.UUID, methodVersion int) error {
	var count int
	query := `SELECT COUNT(*) FROM valuation_method WHERE id = $1 AND version = $2`
	if err := tx.QueryRow(ctx, query, methodID, methodVersion).Scan(&count); err != nil {
		return fmt.Errorf("failed to check valuation method: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: %s v%d", ErrInvalidConversionMethod, methodID, methodVersion)
	}
	return nil
}

func (r *PostgresRegistry) checkSagaExists(ctx context.Context, tx pgx.Tx, prefix string) error {
	var count int
	query := `SELECT COUNT(*) FROM platform_saga_definition WHERE name LIKE $1 AND status = 'ACTIVE'`
	if err := tx.QueryRow(ctx, query, prefix+".%").Scan(&count); err != nil {
		return fmt.Errorf("failed to check saga: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: no active saga starting with %q", ErrSagaNotFound, prefix)
	}
	return nil
}

func (r *PostgresRegistry) loadValuationMethods(ctx context.Context, tx pgx.Tx, accountTypeID uuid.UUID) ([]ValuationMethodTemplate, error) {
	query := `
		SELECT id, account_type_id, input_instrument,
			valuation_method_id, valuation_method_version,
			parameters, status, successor_id,
			created_at, updated_at
		FROM account_type_valuation_methods
		WHERE account_type_id = $1
		ORDER BY input_instrument`

	rows, err := tx.Query(ctx, query, accountTypeID)
	if err != nil {
		return nil, fmt.Errorf("failed to query valuation methods: %w", err)
	}
	defer rows.Close()

	var methods []ValuationMethodTemplate
	for rows.Next() {
		var vmt ValuationMethodTemplate
		var paramsJSON []byte
		var successorID *uuid.UUID

		err := rows.Scan(
			&vmt.ID, &vmt.AccountTypeID, &vmt.InputInstrument,
			&vmt.ValuationMethodID, &vmt.ValuationMethodVersion,
			&paramsJSON, &vmt.Status, &successorID,
			&vmt.CreatedAt, &vmt.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan valuation method: %w", err)
		}

		vmt.SuccessorID = successorID
		if paramsJSON != nil {
			if err := json.Unmarshal(paramsJSON, &vmt.Parameters); err != nil {
				return nil, fmt.Errorf("failed to unmarshal parameters: %w", err)
			}
		}

		methods = append(methods, vmt)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return methods, nil
}
