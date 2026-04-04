package persistence

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
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
)

// ReservationRepository implements domain.ReservationRepository using PostgreSQL.
type ReservationRepository struct {
	pool *pgxpool.Pool
}

// NewReservationRepository creates a new PostgreSQL reservation repository.
func NewReservationRepository(pool *pgxpool.Pool) *ReservationRepository {
	return &ReservationRepository{pool: pool}
}

func (r *ReservationRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	schemaName := pgx.Identifier{tenantID.SchemaName()}.Sanitize()
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// Create persists a new Reservation.
// Returns domain.ErrConflict if a reservation with the same lien_id already exists.
func (r *ReservationRepository) Create(ctx context.Context, reservation *domain.Reservation) error {
	if reservation == nil {
		return fmt.Errorf("create reservation: %w", domain.ErrReservationNotFound)
	}

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

	query := `
		INSERT INTO reservation (
			lien_id, account_id, instrument_code, bucket_id,
			reserved_amount, status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err = tx.Exec(ctx, query,
		reservation.LienID,
		reservation.AccountID,
		reservation.InstrumentCode,
		reservation.BucketID,
		reservation.ReservedAmount,
		reservation.Status.String(),
		reservation.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrConflict
		}
		return fmt.Errorf("failed to insert reservation: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// FindByLienID retrieves a Reservation by its lien_id.
func (r *ReservationRepository) FindByLienID(ctx context.Context, lienID uuid.UUID) (*domain.Reservation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT lien_id, account_id, instrument_code, bucket_id,
			reserved_amount, status, created_at, executed_at, terminated_at
		FROM reservation
		WHERE lien_id = $1`

	var res domain.Reservation
	var status string
	var executedAt, terminatedAt sql.NullTime

	err = tx.QueryRow(ctx, query, lienID).Scan(
		&res.LienID, &res.AccountID, &res.InstrumentCode, &res.BucketID,
		&res.ReservedAmount, &status, &res.CreatedAt, &executedAt, &terminatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrReservationNotFound
		}
		return nil, fmt.Errorf("failed to query reservation: %w", err)
	}

	res.Status = domain.ReservationStatus(status)
	if executedAt.Valid {
		res.ExecutedAt = &executedAt.Time
	}
	if terminatedAt.Valid {
		res.TerminatedAt = &terminatedAt.Time
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &res, nil
}

// UpdateStatus transitions a reservation's status and sets the appropriate timestamp.
func (r *ReservationRepository) UpdateStatus(ctx context.Context, lienID uuid.UUID, newStatus domain.ReservationStatus) error {
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

	now := time.Now().UTC()

	var query string
	var args []any

	switch newStatus {
	case domain.ReservationStatusActive:
		return domain.ErrInvalidReservationState
	case domain.ReservationStatusExecuted:
		query = `UPDATE reservation SET status = $1, executed_at = $2 WHERE lien_id = $3 AND status = 'ACTIVE'`
		args = []any{newStatus.String(), now, lienID}
	case domain.ReservationStatusTerminated:
		query = `UPDATE reservation SET status = $1, terminated_at = $2 WHERE lien_id = $3 AND status = 'ACTIVE'`
		args = []any{newStatus.String(), now, lienID}
	}

	result, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update reservation status: %w", err)
	}

	if result.RowsAffected() == 0 {
		// Check if the reservation exists at all
		var exists bool
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM reservation WHERE lien_id = $1)", lienID).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check reservation existence: %w", err)
		}
		if !exists {
			return domain.ErrReservationNotFound
		}
		return domain.ErrReservationAlreadyFinal
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// SumActiveReservations returns the total reserved amount for active reservations.
// If bucketID is empty, sums across all buckets.
func (r *ReservationRepository) SumActiveReservations(ctx context.Context, accountID, instrumentCode, bucketID string) (decimal.Decimal, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return decimal.Zero, err
	}

	var total decimal.NullDecimal

	if bucketID != "" {
		query := `
			SELECT SUM(reserved_amount)
			FROM reservation
			WHERE account_id = $1
				AND instrument_code = $2
				AND status = 'ACTIVE'
				AND bucket_id = $3`
		err = tx.QueryRow(ctx, query, accountID, instrumentCode, bucketID).Scan(&total)
	} else {
		query := `
			SELECT SUM(reserved_amount)
			FROM reservation
			WHERE account_id = $1
				AND instrument_code = $2
				AND status = 'ACTIVE'`
		err = tx.QueryRow(ctx, query, accountID, instrumentCode).Scan(&total)
	}

	if err != nil {
		return decimal.Zero, fmt.Errorf("failed to sum active reservations: %w", err)
	}

	if !total.Valid {
		return decimal.Zero, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return decimal.Zero, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return total.Decimal, nil
}

// Ensure ReservationRepository implements the interface at compile time.
var _ domain.ReservationRepository = (*ReservationRepository)(nil)
