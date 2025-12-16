package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	// DefaultPageSize is the default number of results returned per page
	DefaultPageSize = 50
	// MaxPageSize is the maximum number of results allowed per page
	MaxPageSize = 1000
)

var decimalHundred = decimal.NewFromInt(100)

// decimalFromCents converts cents (int64) to decimal amount
func decimalFromCents(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimalHundred)
}

var (
	// ErrPostingNotFound is returned when a posting cannot be found
	ErrPostingNotFound = errors.New("ledger posting not found")
	// ErrBookingLogNotFound is returned when a booking log cannot be found
	ErrBookingLogNotFound = errors.New("financial booking log not found")
	// ErrFractionalCents is returned when an amount has fractional cents
	ErrFractionalCents = errors.New("amount has fractional cents that cannot be represented")
	// ErrDuplicateIdempotencyKey is returned when a booking log with the same idempotency key already exists
	ErrDuplicateIdempotencyKey = errors.New("booking log with this idempotency key already exists")
)

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
func (r *LedgerRepository) UpdatePosting(ctx context.Context, posting *domain.LedgerPosting) error {
	entity, err := toPostingEntity(posting)
	if err != nil {
		return err
	}

	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&LedgerPostingEntity{}).
			Where("id = ?", entity.ID).
			Updates(map[string]interface{}{
				"status":         entity.Status,
				"posting_result": entity.PostingResult,
			})

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return ErrPostingNotFound
		}

		return nil
	})
}

// toPostingEntity converts domain model to database entity
func toPostingEntity(posting *domain.LedgerPosting) (LedgerPostingEntity, error) {
	// Convert decimal amount to cents (multiply by 100)
	scaled := posting.Amount.Amount().Mul(decimalHundred)

	// Validate that the amount can be represented exactly in cents (no fractional cents)
	if !scaled.Equal(scaled.Truncate(0)) {
		return LedgerPostingEntity{}, ErrFractionalCents
	}

	amountCents := scaled.IntPart()

	return LedgerPostingEntity{
		ID:                    posting.ID,
		FinancialBookingLogID: posting.FinancialBookingLogID,
		PostingDirection:      string(posting.Direction),
		AmountCents:           amountCents,
		Currency:              string(posting.Amount.Currency()),
		AccountID:             posting.AccountID,
		ValueDate:             posting.ValueDate,
		PostingResult:         posting.PostingResult,
		Status:                string(posting.Status),
		CorrelationID:         posting.CorrelationID,
		CreatedAt:             posting.CreatedAt,
	}, nil
}

// toPostingDomain converts database entity to domain model
func toPostingDomain(entity *LedgerPostingEntity) *domain.LedgerPosting {
	// Convert cents to decimal (divide by 100)
	amount := decimalFromCents(entity.AmountCents)
	money, _ := domain.NewMoney(amount, domain.Currency(entity.Currency))

	return &domain.LedgerPosting{
		ID:                    entity.ID,
		FinancialBookingLogID: entity.FinancialBookingLogID,
		Direction:             domain.PostingDirection(entity.PostingDirection),
		Amount:                money,
		AccountID:             entity.AccountID,
		ValueDate:             entity.ValueDate,
		PostingResult:         entity.PostingResult,
		Status:                domain.TransactionStatus(entity.Status),
		CorrelationID:         entity.CorrelationID,
		CreatedAt:             entity.CreatedAt,
	}
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
func (r *LedgerRepository) UpdateBookingLog(ctx context.Context, log *domain.FinancialBookingLog) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&FinancialBookingLogEntity{}).
			Where("id = ?", log.ID).
			Updates(map[string]interface{}{
				"status":                  string(log.Status),
				"chart_of_accounts_rules": log.ChartOfAccountsRules,
				"updated_at":              log.UpdatedAt,
			})

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return ErrBookingLogNotFound
		}

		return nil
	})
}

// toBookingLogEntity converts domain model to database entity
func toBookingLogEntity(log *domain.FinancialBookingLog, idempotencyKey string) FinancialBookingLogEntity {
	return FinancialBookingLogEntity{
		ID:                      log.ID,
		FinancialAccountType:    log.FinancialAccountType,
		ProductServiceReference: log.ProductServiceReference,
		BusinessUnitReference:   log.BusinessUnitReference,
		ChartOfAccountsRules:    log.ChartOfAccountsRules,
		BaseCurrency:            string(log.BaseCurrency),
		Status:                  string(log.Status),
		IdempotencyKey:          idempotencyKey,
		CreatedAt:               log.CreatedAt,
		UpdatedAt:               log.UpdatedAt,
		Version:                 1,
	}
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

// ListBookingLogs lists booking logs with optional filtering and pagination.
// The context must contain the tenant ID for schema routing.
//
// LIMITATION: Page token parsing is not yet implemented. Pagination currently
// uses OFFSET-based queries which may show inconsistent results if data changes
// between requests. This is suitable for small to medium datasets but should be
// replaced with cursor-based pagination for production use with large datasets.
// See TODO comments in implementation for cursor-based pagination work.
func (r *LedgerRepository) ListBookingLogs(ctx context.Context, params ListBookingLogsParams) (*ListBookingLogsResult, error) {
	// Set default page size if not specified
	pageSize := params.PageSize
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	var result *ListBookingLogsResult
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Build base query
		query := tx.Model(&FinancialBookingLogEntity{})

		// Apply status filter if provided
		if params.StatusFilter != "" {
			query = query.Where("status = ?", params.StatusFilter)
		}

		// Apply business unit filter if provided
		if params.BusinessUnitFilter != "" {
			query = query.Where("business_unit_reference = ?", params.BusinessUnitFilter)
		}

		// Get total count
		var totalCount int64
		if err := query.Count(&totalCount).Error; err != nil {
			return err
		}

		// Apply cursor-based pagination using created_at + id
		// Page token format: <timestamp>_<uuid>
		// TODO(tech-debt-cleanup#1): Implement proper cursor-based pagination
		_ = params.PageToken // Unused for now

		// Fetch results with limit
		var entities []FinancialBookingLogEntity
		err := query.
			Order("created_at DESC, id DESC").
			Limit(pageSize + 1). // Fetch one extra to determine if there's a next page
			Find(&entities).Error
		if err != nil {
			return err
		}

		// Determine if there's a next page
		hasMore := len(entities) > pageSize
		if hasMore {
			entities = entities[:pageSize]
		}

		// Convert to domain models
		bookingLogs := make([]*domain.FinancialBookingLog, len(entities))
		for i, entity := range entities {
			bookingLogs[i] = toBookingLogDomain(&entity)
		}

		// Generate next page token if there are more results
		var nextPageToken string
		if hasMore && len(entities) > 0 {
			lastEntity := entities[len(entities)-1]
			// Simple token format: timestamp_id
			nextPageToken = fmt.Sprintf("%d_%s", lastEntity.CreatedAt.Unix(), lastEntity.ID)
		}

		result = &ListBookingLogsResult{
			BookingLogs:   bookingLogs,
			NextPageToken: nextPageToken,
			TotalCount:    totalCount,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// toBookingLogDomain converts database entity to domain model.
// Note: postings field is unexported and initialized empty.
// Postings are loaded separately to avoid N+1 queries.
func toBookingLogDomain(entity *FinancialBookingLogEntity) *domain.FinancialBookingLog {
	// We need to use NewFinancialBookingLog and then update fields since postings is unexported
	// However, NewFinancialBookingLog creates a new ID, so we reconstruct manually
	log := domain.FinancialBookingLog{
		ID:                      entity.ID,
		FinancialAccountType:    entity.FinancialAccountType,
		ProductServiceReference: entity.ProductServiceReference,
		BusinessUnitReference:   entity.BusinessUnitReference,
		ChartOfAccountsRules:    entity.ChartOfAccountsRules,
		BaseCurrency:            domain.Currency(entity.BaseCurrency),
		Status:                  domain.TransactionStatus(entity.Status),
		CreatedAt:               entity.CreatedAt,
		UpdatedAt:               entity.UpdatedAt,
		// postings initialized as empty slice (loaded separately)
	}
	return &log
}

// ListPostingsParams contains parameters for listing ledger postings
type ListPostingsParams struct {
	// PageSize is the number of results to return (default 50, max 1000)
	PageSize int

	// PageToken is the cursor for pagination (empty for first page)
	PageToken string

	// BookingLogID filters by parent booking log (empty for no filter)
	BookingLogID *uuid.UUID

	// AccountID filters by account identifier (empty for no filter)
	AccountID string

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

// ListPostings lists ledger postings with optional filtering and pagination.
// The context must contain the tenant ID for schema routing.
//
// LIMITATION: Page token parsing is not yet implemented. Pagination currently
// uses OFFSET-based queries which may show inconsistent results if data changes
// between requests. This is suitable for small to medium datasets but should be
// replaced with cursor-based pagination for production use with large datasets.
// See TODO comments in implementation for cursor-based pagination work.
func (r *LedgerRepository) ListPostings(ctx context.Context, params ListPostingsParams) (*ListPostingsResult, error) {
	// Set default page size if not specified
	pageSize := params.PageSize
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	var result *ListPostingsResult
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Build base query
		query := tx.Model(&LedgerPostingEntity{})

		// Apply booking log filter if provided
		if params.BookingLogID != nil {
			query = query.Where("financial_booking_log_id = ?", *params.BookingLogID)
		}

		// Apply account ID filter if provided
		if params.AccountID != "" {
			query = query.Where("account_id = ?", params.AccountID)
		}

		// Apply posting direction filter if provided
		if params.PostingDirection != "" {
			query = query.Where("posting_direction = ?", params.PostingDirection)
		}

		// Apply value date range filters if provided
		if params.ValueDateFrom != nil {
			query = query.Where("value_date >= ?", *params.ValueDateFrom)
		}
		if params.ValueDateTo != nil {
			query = query.Where("value_date <= ?", *params.ValueDateTo)
		}

		// Apply currency filter if provided
		if params.Currency != "" {
			query = query.Where("currency = ?", params.Currency)
		}

		// Apply status filter if provided
		if params.Status != "" {
			query = query.Where("status = ?", params.Status)
		}

		// Get total count
		var totalCount int64
		if err := query.Count(&totalCount).Error; err != nil {
			return err
		}

		// Apply cursor-based pagination using created_at + id
		// Page token format: <timestamp>_<uuid>
		// TODO(tech-debt-cleanup#1): Implement proper cursor-based pagination
		_ = params.PageToken // Unused for now

		// Fetch results with limit
		var entities []LedgerPostingEntity
		err := query.
			Order("created_at DESC, id DESC").
			Limit(pageSize + 1). // Fetch one extra to determine if there's a next page
			Find(&entities).Error
		if err != nil {
			return err
		}

		// Determine if there's a next page
		hasMore := len(entities) > pageSize
		if hasMore {
			entities = entities[:pageSize]
		}

		// Convert to domain models
		postings := make([]*domain.LedgerPosting, len(entities))
		for i, entity := range entities {
			postings[i] = toPostingDomain(&entity)
		}

		// Generate next page token if there are more results
		var nextPageToken string
		if hasMore && len(entities) > 0 {
			lastEntity := entities[len(entities)-1]
			// Simple token format: timestamp_id
			nextPageToken = fmt.Sprintf("%d_%s", lastEntity.CreatedAt.Unix(), lastEntity.ID)
		}

		result = &ListPostingsResult{
			Postings:      postings,
			NextPageToken: nextPageToken,
			TotalCount:    totalCount,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
