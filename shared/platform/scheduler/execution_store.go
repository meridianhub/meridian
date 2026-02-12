package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ExecutionStatus represents the status of a scheduler execution record.
type ExecutionStatus string

// Execution status constants.
const (
	ExecutionStatusTriggered ExecutionStatus = "TRIGGERED"
	ExecutionStatusCompleted ExecutionStatus = "COMPLETED"
	ExecutionStatusFailed    ExecutionStatus = "FAILED"
	ExecutionStatusMissed    ExecutionStatus = "MISSED"
	ExecutionStatusSkipped   ExecutionStatus = "SKIPPED"
)

// ErrNoExecution is returned when no execution record exists for a schedule.
var ErrNoExecution = errors.New("no execution record found")

// Execution represents a row in the scheduler_execution table.
type Execution struct {
	ID            uuid.UUID
	SchedulerName string
	ScheduleID    string
	ScheduledAt   time.Time
	ExecutedAt    *time.Time
	CompletedAt   *time.Time
	Status        ExecutionStatus
	ResultRef     *string
	ErrorMessage  *string
	CreatedAt     time.Time
}

// ExecutionStore provides database operations for scheduler execution records.
type ExecutionStore interface {
	// RecordExecution inserts a new execution record.
	RecordExecution(ctx context.Context, exec Execution) error
	// UpdateExecution updates the status and related fields of an execution.
	UpdateExecution(ctx context.Context, id uuid.UUID, status ExecutionStatus, resultRef *string, errMsg *string) error
	// LastExecution returns the most recent execution for a scheduler+schedule combination.
	LastExecution(ctx context.Context, schedulerName, scheduleID string) (*Execution, error)
}

// PgExecutionStore implements ExecutionStore using pgxpool against CockroachDB.
type PgExecutionStore struct {
	pool *pgxpool.Pool
}

// NewPgExecutionStore creates a new PgExecutionStore.
func NewPgExecutionStore(pool *pgxpool.Pool) *PgExecutionStore {
	return &PgExecutionStore{pool: pool}
}

// RecordExecution inserts a new execution record into the database.
func (s *PgExecutionStore) RecordExecution(ctx context.Context, exec Execution) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if err := s.setSearchPath(ctx, conn); err != nil {
		return err
	}

	query := `
		INSERT INTO scheduler_execution (id, scheduler_name, schedule_id, scheduled_at, executed_at, completed_at, status, result_ref, error_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err = conn.Exec(ctx, query,
		exec.ID, exec.SchedulerName, exec.ScheduleID, exec.ScheduledAt,
		exec.ExecutedAt, exec.CompletedAt, string(exec.Status), exec.ResultRef, exec.ErrorMessage)
	if err != nil {
		return fmt.Errorf("insert execution: %w", err)
	}
	return nil
}

// UpdateExecution updates the status and related fields of an execution record.
func (s *PgExecutionStore) UpdateExecution(ctx context.Context, id uuid.UUID, status ExecutionStatus, resultRef *string, errMsg *string) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if err := s.setSearchPath(ctx, conn); err != nil {
		return err
	}

	now := time.Now().UTC()

	var query string
	var args []any
	switch status {
	case ExecutionStatusCompleted:
		query = `
			UPDATE scheduler_execution
			SET status = $2, completed_at = $3, result_ref = $4, error_message = $5
			WHERE id = $1`
		args = []any{id, string(status), now, resultRef, errMsg}
	default:
		query = `
			UPDATE scheduler_execution
			SET status = $2, executed_at = COALESCE(executed_at, $3), result_ref = $4, error_message = $5
			WHERE id = $1`
		args = []any{id, string(status), now, resultRef, errMsg}
	}

	_, err = conn.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update execution: %w", err)
	}
	return nil
}

// LastExecution returns the most recent execution for a scheduler+schedule combination.
// Returns ErrNoExecution if no execution record exists.
func (s *PgExecutionStore) LastExecution(ctx context.Context, schedulerName, scheduleID string) (*Execution, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if err := s.setSearchPath(ctx, conn); err != nil {
		return nil, err
	}

	query := `
		SELECT id, scheduler_name, schedule_id, scheduled_at, executed_at, completed_at,
		       status, result_ref, error_message, created_at
		FROM scheduler_execution
		WHERE scheduler_name = $1 AND schedule_id = $2
		ORDER BY scheduled_at DESC
		LIMIT 1`

	row := conn.QueryRow(ctx, query, schedulerName, scheduleID)

	var exec Execution
	var status string
	err = row.Scan(
		&exec.ID, &exec.SchedulerName, &exec.ScheduleID, &exec.ScheduledAt,
		&exec.ExecutedAt, &exec.CompletedAt, &status, &exec.ResultRef,
		&exec.ErrorMessage, &exec.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoExecution
		}
		return nil, fmt.Errorf("scan execution: %w", err)
	}
	exec.Status = ExecutionStatus(status)
	return &exec, nil
}

// setSearchPath sets the tenant schema on the connection if present in context.
func (s *PgExecutionStore) setSearchPath(ctx context.Context, conn *pgxpool.Conn) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}
	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	_, err := conn.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName))
	if err != nil {
		return fmt.Errorf("set tenant schema: %w", err)
	}
	return nil
}
