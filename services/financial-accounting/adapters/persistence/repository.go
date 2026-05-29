package persistence

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

const (
	// DefaultPageSize is the default number of results returned per page
	DefaultPageSize = 50
	// MaxPageSize is the maximum number of results allowed per page
	MaxPageSize = 1000
)

var (
	// ErrPostingNotFound is returned when a posting cannot be found
	ErrPostingNotFound = errors.New("ledger posting not found")
	// ErrBookingLogNotFound is returned when a booking log cannot be found
	ErrBookingLogNotFound = errors.New("financial booking log not found")
	// ErrFractionalCents is returned when an amount has fractional cents
	ErrFractionalCents = errors.New("amount has fractional cents that cannot be represented")
	// ErrDuplicateIdempotencyKey is returned when a booking log with the same idempotency key already exists
	ErrDuplicateIdempotencyKey = errors.New("booking log with this idempotency key already exists")
	// ErrInvalidPageToken is returned when the pagination token has an invalid format
	ErrInvalidPageToken = errors.New("invalid page token format")
)

// Timestamp bounds for security validation.
// Financial records before Unix epoch (1970) or far in the future are unexpected
// and could indicate token manipulation.
var (
	minValidTimestamp = int64(0)                                           // Unix epoch (1970-01-01)
	maxValidTimestamp = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix() // Year 2100
)

// parseCursorToken parses a pagination token in format "timestamp_uuid".
// Returns the timestamp and UUID, or an error if the format is invalid.
// An empty token returns zero values with no error (indicating first page).
func parseCursorToken(token string) (time.Time, uuid.UUID, error) {
	if token == "" {
		return time.Time{}, uuid.Nil, nil
	}

	// Use SplitN to handle edge cases where UUID might theoretically contain underscore
	// (though standard UUIDs use hyphens). This ensures we only split on the first underscore.
	parts := strings.SplitN(token, "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return time.Time{}, uuid.Nil, ErrInvalidPageToken
	}

	// Parse timestamp
	timestampUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: invalid timestamp", ErrInvalidPageToken)
	}

	// Validate timestamp bounds for security - financial records should be within reasonable range
	if timestampUnix < minValidTimestamp || timestampUnix > maxValidTimestamp {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: timestamp out of valid range", ErrInvalidPageToken)
	}

	timestamp := time.Unix(timestampUnix, 0).UTC()

	// Parse UUID
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: invalid uuid", ErrInvalidPageToken)
	}

	return timestamp, id, nil
}

// applyCursorPagination applies cursor-based pagination to a GORM query.
// This helper reduces duplication between ListBookingLogs and ListPostings.
//
// The cursor uses date_trunc('second', created_at) for comparison because the
// cursor token stores Unix timestamp (second precision). This ensures consistent
// ordering between the ORDER BY and WHERE clauses.
//
// Performance note: Using date_trunc() prevents use of standard B-tree indexes
// on created_at. For large datasets, consider either:
//   - Creating a functional index: CREATE INDEX idx_<table>_cursor ON <table>
//     (date_trunc('second', created_at) DESC, id DESC);
//   - Storing millisecond-precision timestamps in tokens (e.g., "1734567890123_uuid")
//     to avoid date_trunc entirely and use standard B-tree indexes.
func applyCursorPagination(query *gorm.DB, cursorTime time.Time, cursorID uuid.UUID) *gorm.DB {
	if cursorTime.IsZero() {
		return query
	}
	return query.Where(
		"(date_trunc('second', created_at) < ?) OR (date_trunc('second', created_at) = ? AND id < ?)",
		cursorTime, cursorTime, cursorID,
	)
}

// LedgerRepository provides persistence operations for ledger postings
type LedgerRepository struct {
	db *gorm.DB
}

// NewLedgerRepository creates a new repository instance
func NewLedgerRepository(gormDB *gorm.DB) *LedgerRepository {
	return &LedgerRepository{db: gormDB}
}

// withTenantScope returns a GORM DB instance scoped to the tenant from context.
// The system is always in multi-tenant mode and requires tenant context.
// This must be called within a transaction for the search_path setting to work correctly.
func (r *LedgerRepository) withTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	return db.WithGormTenantScope(ctx, tx)
}

// withTenantTransaction executes the given function with tenant scoping.
// The system is always in multi-tenant mode, so this wraps the function in a transaction
// and sets the search_path. This helper reduces code duplication across repository methods.
func (r *LedgerRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// SavePosting persists a ledger posting.
// The context must contain the tenant ID for schema routing.
func (r *LedgerRepository) SavePosting(ctx context.Context, posting *domain.LedgerPosting) error {
	entity, err := toPostingEntity(posting)
	if err != nil {
		return err
	}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Create(&entity).Error
	})
}

// SavePostingsInTransaction persists multiple postings atomically within a transaction.
// The context must contain the tenant ID for schema routing.
func (r *LedgerRepository) SavePostingsInTransaction(ctx context.Context, postings []*domain.LedgerPosting) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Set tenant scope (always required in multi-tenant mode)
		var scopedTx *gorm.DB
		var scopeErr error
		scopedTx, scopeErr = r.withTenantScope(ctx, tx)
		if scopeErr != nil {
			return scopeErr
		}

		for _, posting := range postings {
			entity, err := toPostingEntity(posting)
			if err != nil {
				return err
			}
			if err := scopedTx.Create(&entity).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}
	return nil
}

// GetPosting retrieves a posting by ID.
// The context must contain the tenant ID for schema routing.
func (r *LedgerRepository) GetPosting(ctx context.Context, id uuid.UUID) (*domain.LedgerPosting, error) {
	var posting *domain.LedgerPosting
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity LedgerPostingEntity
		result := tx.First(&entity, "id = ?", id)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPostingNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		posting = toPostingDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return posting, nil
}

// GetPostingsByBookingLogID retrieves all postings for a booking log.
// The context must contain the tenant ID for schema routing.
func (r *LedgerRepository) GetPostingsByBookingLogID(ctx context.Context, bookingLogID uuid.UUID) ([]*domain.LedgerPosting, error) {
	var postings []*domain.LedgerPosting
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []LedgerPostingEntity
		result := tx.Where("financial_booking_log_id = ?", bookingLogID).
			Order("created_at ASC").
			Find(&entities)
		if result.Error != nil {
			return result.Error
		}

		postings = make([]*domain.LedgerPosting, len(entities))
		for i, entity := range entities {
			postings[i] = toPostingDomain(&entity)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return postings, nil
}

// UpdatePosting updates an existing ledger posting.
// The context must contain the tenant ID for schema routing.
// Uses Save() to trigger GORM hooks for audit logging.
func (r *LedgerRepository) UpdatePosting(ctx context.Context, posting *domain.LedgerPosting) error {
	entity, err := toPostingEntity(posting)
	if err != nil {
		return err
	}

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Fetch existing entity to apply updates (required for Save() to trigger hooks)
		var existing LedgerPostingEntity
		if err := tx.First(&existing, "id = ?", entity.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPostingNotFound
			}
			return err
		}

		// Apply partial updates
		existing.Status = entity.Status
		existing.PostingResult = entity.PostingResult
		existing.UpdatedAt = time.Now()

		// Use Save() to trigger GORM hooks (BeforeUpdate, AfterUpdate)
		return tx.Save(&existing).Error
	})
}

// GetBookingLog retrieves a booking log by ID.
// The context must contain the tenant ID for schema routing.
func (r *LedgerRepository) GetBookingLog(ctx context.Context, id uuid.UUID) (*domain.FinancialBookingLog, error) {
	var bookingLog *domain.FinancialBookingLog
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity FinancialBookingLogEntity
		result := tx.First(&entity, "id = ?", id)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrBookingLogNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		bookingLog = toBookingLogDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return bookingLog, nil
}

// SaveBookingLog persists a new financial booking log.
// Returns ErrDuplicateIdempotencyKey if a booking log with the same idempotency key already exists.
// The context must contain the tenant ID for schema routing.
func (r *LedgerRepository) SaveBookingLog(ctx context.Context, log *domain.FinancialBookingLog, idempotencyKey string) error {
	entity := toBookingLogEntity(log, idempotencyKey)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		err := tx.Create(&entity).Error
		if err != nil {
			// Check for unique constraint violation using PostgreSQL error code
			// 23505 is the SQLSTATE for unique_violation
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				if strings.Contains(pgErr.ConstraintName, "idempotency_key") {
					return ErrDuplicateIdempotencyKey
				}
			}
			return err
		}
		return nil
	})
}

// UpdateBookingLog updates an existing financial booking log.
// The context must contain the tenant ID for schema routing.
// Uses Save() to trigger GORM hooks for audit logging.
func (r *LedgerRepository) UpdateBookingLog(ctx context.Context, log *domain.FinancialBookingLog) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Fetch existing entity to apply updates (required for Save() to trigger hooks)
		var existing FinancialBookingLogEntity
		if err := tx.First(&existing, "id = ?", log.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBookingLogNotFound
			}
			return err
		}

		// Apply partial updates
		existing.Status = string(log.Status)
		existing.ChartOfAccountsRules = log.ChartOfAccountsRules
		existing.UpdatedAt = log.UpdatedAt

		// Use Save() to trigger GORM hooks (BeforeUpdate, AfterUpdate)
		return tx.Save(&existing).Error
	})
}

// ListBookingLogsParams contains parameters for listing booking logs
type ListBookingLogsParams struct {
	// PageSize is the number of results to return (default 50, max 1000)
	PageSize int

	// PageToken is the cursor for pagination (empty for first page)
	PageToken string

	// StatusFilter filters by transaction status (empty for no filter)
	StatusFilter string

	// BusinessUnitFilter filters by business unit (empty for no filter)
	BusinessUnitFilter string
}

// ListBookingLogsResult contains the results of a list operation
type ListBookingLogsResult struct {
	BookingLogs   []*domain.FinancialBookingLog
	NextPageToken string
	TotalCount    int64
}

// ListBookingLogs lists booking logs with cursor-based pagination and optional filtering.
// The context must contain the tenant ID for schema routing.
//
// Pagination uses a cursor approach with created_at timestamp and id for stable,
// consistent results even when data changes between requests. The page token format
// is "timestamp_uuid" representing the last item from the previous page.
func (r *LedgerRepository) ListBookingLogs(ctx context.Context, params ListBookingLogsParams) (*ListBookingLogsResult, error) {
	pageSize := clampPageSize(params.PageSize)

	cursorTime, cursorID, err := parseCursorToken(params.PageToken)
	if err != nil {
		return nil, err
	}

	var result *ListBookingLogsResult
	err = r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&FinancialBookingLogEntity{})
		query = applyBookingLogFilters(query, params)

		var totalCount int64
		if err := query.Count(&totalCount).Error; err != nil {
			return err
		}

		query = applyCursorPagination(query, cursorTime, cursorID)

		var entities []FinancialBookingLogEntity
		err := query.
			Order("date_trunc('second', created_at) DESC, id DESC").
			Limit(pageSize + 1).
			Find(&entities).Error
		if err != nil {
			return err
		}

		result = buildBookingLogResult(entities, pageSize, totalCount)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// applyBookingLogFilters applies optional status and business unit filters to the query.
func applyBookingLogFilters(query *gorm.DB, params ListBookingLogsParams) *gorm.DB {
	if params.StatusFilter != "" {
		query = query.Where("status = ?", params.StatusFilter)
	}
	if params.BusinessUnitFilter != "" {
		query = query.Where("business_unit_reference = ?", params.BusinessUnitFilter)
	}
	return query
}

// buildBookingLogResult converts entities to domain models and generates the next page token.
func buildBookingLogResult(entities []FinancialBookingLogEntity, pageSize int, totalCount int64) *ListBookingLogsResult {
	hasMore := len(entities) > pageSize
	if hasMore {
		entities = entities[:pageSize]
	}

	bookingLogs := make([]*domain.FinancialBookingLog, len(entities))
	for i, entity := range entities {
		bookingLogs[i] = toBookingLogDomain(&entity)
	}

	var nextPageToken string
	if hasMore && len(entities) > 0 {
		last := entities[len(entities)-1]
		nextPageToken = fmt.Sprintf("%d_%s", last.CreatedAt.Unix(), last.ID)
	}

	return &ListBookingLogsResult{
		BookingLogs:   bookingLogs,
		NextPageToken: nextPageToken,
		TotalCount:    totalCount,
	}
}

// clampPageSize applies default and max bounds to the requested page size.
func clampPageSize(pageSize int) int {
	if pageSize == 0 {
		return DefaultPageSize
	}
	if pageSize > MaxPageSize {
		return MaxPageSize
	}
	return pageSize
}

// ListPostingsParams contains parameters for listing ledger postings
type ListPostingsParams struct {
	// PageSize is the number of results to return (default 50, max 1000)
	PageSize int

	// PageToken is the cursor for pagination (empty for first page)
	PageToken string

	// BookingLogID filters by parent booking log (empty for no filter)
	BookingLogID *uuid.UUID

	// AccountID filters by account identifier (empty for no filter). Ignored when AccountIDs is non-empty.
	AccountID string

	// AccountIDs filters by multiple account identifiers (empty for no filter). Takes precedence over AccountID.
	AccountIDs []string

	// PostingDirection filters by DEBIT or CREDIT (empty for no filter)
	PostingDirection string

	// ValueDateFrom filters postings on or after this date (nil for no filter)
	ValueDateFrom *time.Time

	// ValueDateTo filters postings on or before this date (nil for no filter)
	ValueDateTo *time.Time

	// Currency filters by currency code (empty for no filter)
	Currency string

	// Status filters by transaction status (empty for no filter)
	Status string
}

// ListPostingsResult contains the results of a list operation
type ListPostingsResult struct {
	Postings      []*domain.LedgerPosting
	NextPageToken string
	TotalCount    int64
}

// ListPostings lists ledger postings with cursor-based pagination and optional filtering.
// The context must contain the tenant ID for schema routing.
//
// Pagination uses a cursor approach with created_at timestamp and id for stable,
// consistent results even when data changes between requests. The page token format
// is "timestamp_uuid" representing the last item from the previous page.
func (r *LedgerRepository) ListPostings(ctx context.Context, params ListPostingsParams) (*ListPostingsResult, error) {
	pageSize := clampPageSize(params.PageSize)

	cursorTime, cursorID, err := parseCursorToken(params.PageToken)
	if err != nil {
		return nil, err
	}

	var result *ListPostingsResult
	err = r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&LedgerPostingEntity{})
		query = applyPostingFilters(query, params)

		var totalCount int64
		if err := query.Count(&totalCount).Error; err != nil {
			return err
		}

		query = applyCursorPagination(query, cursorTime, cursorID)

		var entities []LedgerPostingEntity
		err := query.
			Order("date_trunc('second', created_at) DESC, id DESC").
			Limit(pageSize + 1).
			Find(&entities).Error
		if err != nil {
			return err
		}

		result = buildPostingResult(entities, pageSize, totalCount)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// applyPostingFilters applies all optional filters to a posting query.
func applyPostingFilters(query *gorm.DB, params ListPostingsParams) *gorm.DB {
	if params.BookingLogID != nil {
		query = query.Where("financial_booking_log_id = ?", *params.BookingLogID)
	}
	if len(params.AccountIDs) > 0 {
		query = query.Where("account_id IN ?", params.AccountIDs)
	} else if params.AccountID != "" {
		query = query.Where("account_id = ?", params.AccountID)
	}
	if params.PostingDirection != "" {
		query = query.Where("posting_direction = ?", params.PostingDirection)
	}
	if params.ValueDateFrom != nil {
		query = query.Where("value_date >= ?", *params.ValueDateFrom)
	}
	if params.ValueDateTo != nil {
		query = query.Where("value_date <= ?", *params.ValueDateTo)
	}
	if params.Currency != "" {
		query = query.Where("currency = ?", params.Currency)
	}
	if params.Status != "" {
		query = query.Where("status = ?", params.Status)
	}
	return query
}

// buildPostingResult converts entities to domain models and generates the next page token.
func buildPostingResult(entities []LedgerPostingEntity, pageSize int, totalCount int64) *ListPostingsResult {
	hasMore := len(entities) > pageSize
	if hasMore {
		entities = entities[:pageSize]
	}

	postings := make([]*domain.LedgerPosting, len(entities))
	for i, entity := range entities {
		postings[i] = toPostingDomain(&entity)
	}

	var nextPageToken string
	if hasMore && len(entities) > 0 {
		last := entities[len(entities)-1]
		nextPageToken = fmt.Sprintf("%d_%s", last.CreatedAt.Unix(), last.ID)
	}

	return &ListPostingsResult{
		Postings:      postings,
		NextPageToken: nextPageToken,
		TotalCount:    totalCount,
	}
}

// WithTransaction executes a function within a database transaction with tenant scoping.
// This is used for implementing the transactional outbox pattern where both the entity
// update and the outbox event write must succeed or fail together atomically.
//
// The provided function receives a tenant-scoped *gorm.DB transaction that can be used
// for all database operations within the transaction.
//
// Example:
//
//	err := repo.WithTransaction(ctx, func(tx *gorm.DB) error {
//	    if err := tx.Save(&entity).Error; err != nil {
//	        return err
//	    }
//	    return outboxRepo.Insert(ctx, tx, event)
//	})
func (r *LedgerRepository) WithTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return r.withTenantTransaction(ctx, fn)
}

// DB returns the underlying GORM database instance.
// This is primarily used for passing the DB to other components that need
// database access, such as the outbox repository for the transactional outbox pattern.
func (r *LedgerRepository) DB() *gorm.DB {
	return r.db
}
