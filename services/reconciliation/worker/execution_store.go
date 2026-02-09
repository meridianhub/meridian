package worker

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

// Execution status values for scheduler execution records.
const (
	ExecutionStatusTriggered ExecutionStatus = "TRIGGERED"
	ExecutionStatusCompleted ExecutionStatus = "COMPLETED"
	ExecutionStatusFailed    ExecutionStatus = "FAILED"
	ExecutionStatusMissed    ExecutionStatus = "MISSED"
	ExecutionStatusSkipped   ExecutionStatus = "SKIPPED"
)

// ErrNoExecution is returned when no execution record exists for a schedule.
var ErrNoExecution = errors.New("no execution record found")

// SchedulerExecution represents a row in the scheduler_execution table.
type SchedulerExecution struct {
	ID           uuid.UUID
	ScheduleName string
	ScheduledAt  time.Time
	ExecutedAt   *time.Time
	Status       ExecutionStatus
	RunID        *uuid.UUID
	ErrorMessage *string
	CreatedAt    time.Time
}

// ExecutionStore provides database operations for scheduler execution records.
type ExecutionStore interface {
	// RecordExecution inserts a new execution record.
	RecordExecution(ctx context.Context, exec SchedulerExecution) error
	// UpdateExecution updates the status and related fields of an execution.
	UpdateExecution(ctx context.Context, id uuid.UUID, status ExecutionStatus, runID *uuid.UUID, errMsg *string) error
	// LastExecution returns the most recent execution for a schedule name, or nil if none.
	LastExecution(ctx context.Context, scheduleName string) (*SchedulerExecution, error)
}

// PgExecutionStore implements ExecutionStore using pgxpool.
type PgExecutionStore struct {
	pool *pgxpool.Pool
}

// NewPgExecutionStore creates a new PgExecutionStore.
func NewPgExecutionStore(pool *pgxpool.Pool) *PgExecutionStore {
	return &PgExecutionStore{pool: pool}
}

// RecordExecution inserts a new execution record into the database.
func (s *PgExecutionStore) RecordExecution(ctx context.Context, exec SchedulerExecution) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if err := s.setSearchPath(ctx, conn); err != nil {
		return err
	}

	query := `
		INSERT INTO scheduler_execution (id, schedule_name, scheduled_at, executed_at, status, run_id, error_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err = conn.Exec(ctx, query,
		exec.ID, exec.ScheduleName, exec.ScheduledAt, exec.ExecutedAt,
		string(exec.Status), exec.RunID, exec.ErrorMessage)
	if err != nil {
		return fmt.Errorf("insert execution: %w", err)
	}
	return nil
}

// UpdateExecution updates the status and related fields of an execution record.
func (s *PgExecutionStore) UpdateExecution(ctx context.Context, id uuid.UUID, status ExecutionStatus, runID *uuid.UUID, errMsg *string) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if err := s.setSearchPath(ctx, conn); err != nil {
		return err
	}

	now := time.Now().UTC()
	query := `
		UPDATE scheduler_execution
		SET status = $2, run_id = $3, error_message = $4, executed_at = $5
		WHERE id = $1`

	_, err = conn.Exec(ctx, query, id, string(status), runID, errMsg, now)
	if err != nil {
		return fmt.Errorf("update execution: %w", err)
	}
	return nil
}

// LastExecution returns the most recent execution for a schedule name.
// Returns ErrNoExecution if no execution record exists.
func (s *PgExecutionStore) LastExecution(ctx context.Context, scheduleName string) (*SchedulerExecution, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if err := s.setSearchPath(ctx, conn); err != nil {
		return nil, err
	}

	query := `
		SELECT id, schedule_name, scheduled_at, executed_at, status, run_id, error_message, created_at
		FROM scheduler_execution
		WHERE schedule_name = $1
		ORDER BY scheduled_at DESC
		LIMIT 1`

	row := conn.QueryRow(ctx, query, scheduleName)

	var exec SchedulerExecution
	var status string
	err = row.Scan(
		&exec.ID, &exec.ScheduleName, &exec.ScheduledAt, &exec.ExecutedAt,
		&status, &exec.RunID, &exec.ErrorMessage, &exec.CreatedAt,
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
