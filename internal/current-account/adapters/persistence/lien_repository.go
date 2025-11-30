package persistence

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"gorm.io/gorm"
)

// Lien repository errors
var (
	ErrLienNotFound             = errors.New("lien not found")
	ErrLienVersionConflict      = errors.New("version conflict: lien was modified by another transaction")
	ErrLienCurrencyInconsistent = errors.New("active liens have inconsistent currencies")
)

// LienRepository provides persistence operations for liens
type LienRepository struct {
	db *gorm.DB
}

// NewLienRepository creates a new lien repository
func NewLienRepository(db *gorm.DB) *LienRepository {
	return &LienRepository{db: db}
}

// WithTx returns a new LienRepository that uses the provided transaction.
// This enables multiple repository operations within a single transaction.
func (r *LienRepository) WithTx(tx *gorm.DB) *LienRepository {
	return &LienRepository{db: tx}
}

// Create inserts a new lien
func (r *LienRepository) Create(lien *domain.Lien) error {
	entity := toLienEntity(lien)
	return r.db.Create(entity).Error
}

// FindByID retrieves a lien by its UUID
func (r *LienRepository) FindByID(id uuid.UUID) (*domain.Lien, error) {
	var entity LienEntity
	result := r.db.Where("id = ?", id).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrLienNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toLienDomain(&entity)
}

// FindByAccountID retrieves all liens for an account
func (r *LienRepository) FindByAccountID(accountID uuid.UUID) ([]*domain.Lien, error) {
	var entities []LienEntity
	result := r.db.Where("account_id = ?", accountID).Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	liens := make([]*domain.Lien, 0, len(entities))
	for _, entity := range entities {
		lien, err := toLienDomain(&entity)
		if err != nil {
			return nil, err
		}
		liens = append(liens, lien)
	}

	return liens, nil
}

// FindActiveByAccountID retrieves all active non-expired liens for an account.
// Liens with status ACTIVE but past their expires_at are excluded.
func (r *LienRepository) FindActiveByAccountID(accountID uuid.UUID) ([]*domain.Lien, error) {
	var entities []LienEntity
	result := r.db.Where(
		"account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
		accountID, string(domain.LienStatusActive), time.Now(),
	).Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	liens := make([]*domain.Lien, 0, len(entities))
	for _, entity := range entities {
		lien, err := toLienDomain(&entity)
		if err != nil {
			return nil, err
		}
		liens = append(liens, lien)
	}

	return liens, nil
}

// FindByPaymentOrderReference retrieves a lien by its payment order reference
func (r *LienRepository) FindByPaymentOrderReference(reference string) (*domain.Lien, error) {
	var entity LienEntity
	result := r.db.Where("payment_order_reference = ?", reference).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrLienNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toLienDomain(&entity)
}

// Update updates an existing lien with optimistic locking
func (r *LienRepository) Update(lien *domain.Lien) error {
	entity := toLienEntity(lien)

	// Optimistic locking: use WHERE clause with version check
	result := r.db.Model(&LienEntity{}).
		Where("id = ? AND version = ?", entity.ID, lien.Version).
		Updates(map[string]interface{}{
			"status":             entity.Status,
			"termination_reason": entity.TerminationReason,
			"updated_at":         entity.UpdatedAt,
			"version":            lien.Version + 1,
		})

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return ErrLienVersionConflict
	}

	// Update domain model version
	lien.Version = lien.Version + 1

	return nil
}

// SumActiveAmountByAccountID returns the total amount of active non-expired liens for an account in cents.
// Returns ErrLienCurrencyInconsistent if liens with different currencies exist (indicates data corruption).
// Currency validation is enforced at the service layer when creating liens (InitiateLien).
func (r *LienRepository) SumActiveAmountByAccountID(accountID uuid.UUID) (int64, error) {
	// Capture timestamp once to ensure consistency between the two queries
	now := time.Now()

	// First, check for currency consistency (defensive check for data corruption)
	var currencyCount int64
	countResult := r.db.Model(&LienEntity{}).
		Where("account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
			accountID, string(domain.LienStatusActive), now).
		Select("COUNT(DISTINCT currency)").
		Scan(&currencyCount)

	if countResult.Error != nil {
		return 0, countResult.Error
	}

	if currencyCount > 1 {
		return 0, ErrLienCurrencyInconsistent
	}

	// Sum active non-expired liens
	var totalCents int64
	result := r.db.Model(&LienEntity{}).
		Where("account_id = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
			accountID, string(domain.LienStatusActive), now).
		Select("COALESCE(SUM(amount_cents), 0)").
		Scan(&totalCents)

	if result.Error != nil {
		return 0, result.Error
	}

	return totalCents, nil
}

// toLienEntity converts domain model to database entity
func toLienEntity(lien *domain.Lien) *LienEntity {
	return &LienEntity{
		ID:                    lien.ID,
		AccountID:             lien.AccountID,
		AmountCents:           lien.Amount.AmountCents(),
		Currency:              lien.Amount.Currency(),
		Status:                string(lien.Status),
		PaymentOrderReference: lien.PaymentOrderReference,
		TerminationReason:     lien.TerminationReason,
		ExpiresAt:             lien.ExpiresAt,
		CreatedAt:             lien.CreatedAt,
		UpdatedAt:             lien.UpdatedAt,
		Version:               lien.Version,
	}
}

// toLienDomain converts database entity to domain model
func toLienDomain(entity *LienEntity) (*domain.Lien, error) {
	amount, err := domain.NewMoney(entity.Currency, entity.AmountCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create lien amount from database: %w", err)
	}

	return &domain.Lien{
		ID:                    entity.ID,
		AccountID:             entity.AccountID,
		Amount:                amount,
		Status:                domain.LienStatus(entity.Status),
		PaymentOrderReference: entity.PaymentOrderReference,
		TerminationReason:     entity.TerminationReason,
		ExpiresAt:             entity.ExpiresAt,
		Version:               entity.Version,
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
	}, nil
}
