package persistence

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/financial-accounting/domain"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
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
)

// LedgerRepository provides persistence operations for ledger postings
type LedgerRepository struct {
	db *gorm.DB
}

// NewLedgerRepository creates a new repository instance
func NewLedgerRepository(db *gorm.DB) *LedgerRepository {
	return &LedgerRepository{db: db}
}

// SavePosting persists a ledger posting
func (r *LedgerRepository) SavePosting(ctx context.Context, posting *domain.LedgerPosting) error {
	entity, err := toPostingEntity(posting)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(&entity).Error
}

// SavePostingsInTransaction persists multiple postings atomically within a transaction
func (r *LedgerRepository) SavePostingsInTransaction(ctx context.Context, postings []*domain.LedgerPosting) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, posting := range postings {
			entity, err := toPostingEntity(posting)
			if err != nil {
				return err
			}
			if err := tx.Create(&entity).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetPosting retrieves a posting by ID
func (r *LedgerRepository) GetPosting(ctx context.Context, id uuid.UUID) (*domain.LedgerPosting, error) {
	var entity LedgerPostingEntity
	err := r.db.WithContext(ctx).First(&entity, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPostingNotFound
	}
	if err != nil {
		return nil, err
	}

	return toPostingDomain(&entity), nil
}

// GetPostingsByBookingLogID retrieves all postings for a booking log
func (r *LedgerRepository) GetPostingsByBookingLogID(ctx context.Context, bookingLogID uuid.UUID) ([]*domain.LedgerPosting, error) {
	var entities []LedgerPostingEntity
	err := r.db.WithContext(ctx).
		Where("financial_booking_log_id = ?", bookingLogID).
		Order("created_at ASC").
		Find(&entities).Error
	if err != nil {
		return nil, err
	}

	postings := make([]*domain.LedgerPosting, len(entities))
	for i, entity := range entities {
		postings[i] = toPostingDomain(&entity)
	}

	return postings, nil
}

// toPostingEntity converts domain model to database entity
func toPostingEntity(posting *domain.LedgerPosting) (LedgerPostingEntity, error) {
	// Convert decimal amount to cents (multiply by 100)
	scaled := posting.Amount.Amount.Mul(decimalHundred)

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
		Currency:              string(posting.Amount.Currency),
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
