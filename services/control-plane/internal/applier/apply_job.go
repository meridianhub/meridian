// Package applier provides the manifest application orchestrator that executes
// the planned gRPC calls as a durable Starlark saga with automatic compensation.
package applier

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplyJobStatus represents the lifecycle state of a manifest apply job.
type ApplyJobStatus string

const (
	// ApplyJobStatusPending indicates the job has been created but not yet started.
	ApplyJobStatusPending ApplyJobStatus = "PENDING"
	// ApplyJobStatusApplying indicates the job is currently executing.
	ApplyJobStatusApplying ApplyJobStatus = "APPLYING"
	// ApplyJobStatusApplied indicates the job completed successfully.
	ApplyJobStatusApplied ApplyJobStatus = "APPLIED"
	// ApplyJobStatusFailed indicates the job failed.
	ApplyJobStatusFailed ApplyJobStatus = "FAILED"
)

// ApplyJob represents a manifest application job.
type ApplyJob struct {
	ID              uuid.UUID
	ManifestVersion int
	SagaExecutionID *uuid.UUID
	Status          ApplyJobStatus
	Error           string
	CreatedAt       time.Time
	CompletedAt     *time.Time
}

// ApplyJob errors.
var (
	// ErrJobNotFound is returned when a job is not found.
	ErrJobNotFound = errors.New("apply job not found")
)

// ApplyJobRepository provides persistence for manifest apply jobs.
type ApplyJobRepository struct {
	pool *pgxpool.Pool
}

// NewApplyJobRepository creates a new ApplyJobRepository.
func NewApplyJobRepository(pool *pgxpool.Pool) *ApplyJobRepository {
	return &ApplyJobRepository{pool: pool}
}

// Create creates a new manifest apply job in PENDING status.
func (r *ApplyJobRepository) Create(ctx context.Context, manifestVersion int) (*ApplyJob, error) {
	job := &ApplyJob{
		ID:              uuid.New(),
		ManifestVersion: manifestVersion,
		Status:          ApplyJobStatusPending,
		CreatedAt:       time.Now(),
	}

	_, err := r.pool.Exec(ctx,
		`INSERT INTO manifest_apply_job (id, manifest_version, status, created_at)
		 VALUES ($1, $2, $3, $4)`,
		job.ID, job.ManifestVersion, string(job.Status), job.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create apply job: %w", err)
	}

	return job, nil
}

// MarkApplying transitions a job to APPLYING status and links it to a saga execution.
func (r *ApplyJobRepository) MarkApplying(ctx context.Context, jobID uuid.UUID, sagaExecutionID uuid.UUID) error {
	result, err := r.pool.Exec(ctx,
		`UPDATE manifest_apply_job
		 SET status = $1, saga_execution_id = $2
		 WHERE id = $3 AND status = $4`,
		string(ApplyJobStatusApplying), sagaExecutionID, jobID, string(ApplyJobStatusPending),
	)
	if err != nil {
		return fmt.Errorf("mark applying: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrJobNotFound
	}
	return nil
}

// MarkApplied transitions a job to APPLIED status.
func (r *ApplyJobRepository) MarkApplied(ctx context.Context, jobID uuid.UUID) error {
	now := time.Now()
	result, err := r.pool.Exec(ctx,
		`UPDATE manifest_apply_job
		 SET status = $1, completed_at = $2
		 WHERE id = $3 AND status = $4`,
		string(ApplyJobStatusApplied), now, jobID, string(ApplyJobStatusApplying),
	)
	if err != nil {
		return fmt.Errorf("mark applied: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrJobNotFound
	}
	return nil
}

// MarkFailed transitions a job to FAILED status with an error message.
func (r *ApplyJobRepository) MarkFailed(ctx context.Context, jobID uuid.UUID, errMsg string) error {
	now := time.Now()
	result, err := r.pool.Exec(ctx,
		`UPDATE manifest_apply_job
		 SET status = $1, error = $2, completed_at = $3
		 WHERE id = $4 AND status IN ($5, $6)`,
		string(ApplyJobStatusFailed), errMsg, now, jobID,
		string(ApplyJobStatusPending), string(ApplyJobStatusApplying),
	)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrJobNotFound
	}
	return nil
}

// GetByID retrieves a job by its ID.
func (r *ApplyJobRepository) GetByID(ctx context.Context, jobID uuid.UUID) (*ApplyJob, error) {
	var job ApplyJob
	var status string
	var sagaExecID *uuid.UUID
	var errMsg *string

	err := r.pool.QueryRow(ctx,
		`SELECT id, manifest_version, saga_execution_id, status, error, created_at, completed_at
		 FROM manifest_apply_job WHERE id = $1`,
		jobID,
	).Scan(&job.ID, &job.ManifestVersion, &sagaExecID, &status, &errMsg, &job.CreatedAt, &job.CompletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("get apply job: %w", err)
	}

	job.Status = ApplyJobStatus(status)
	job.SagaExecutionID = sagaExecID
	if errMsg != nil {
		job.Error = *errMsg
	}

	return &job, nil
}

// GetByManifestVersion retrieves the latest job for a manifest version.
func (r *ApplyJobRepository) GetByManifestVersion(ctx context.Context, manifestVersion int) (*ApplyJob, error) {
	var job ApplyJob
	var status string
	var sagaExecID *uuid.UUID
	var errMsg *string

	err := r.pool.QueryRow(ctx,
		`SELECT id, manifest_version, saga_execution_id, status, error, created_at, completed_at
		 FROM manifest_apply_job
		 WHERE manifest_version = $1
		 ORDER BY created_at DESC
		 LIMIT 1`,
		manifestVersion,
	).Scan(&job.ID, &job.ManifestVersion, &sagaExecID, &status, &errMsg, &job.CreatedAt, &job.CompletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("get apply job by version: %w", err)
	}

	job.Status = ApplyJobStatus(status)
	job.SagaExecutionID = sagaExecID
	if errMsg != nil {
		job.Error = *errMsg
	}

	return &job, nil
}
