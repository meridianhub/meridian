package persistence

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	cadomain "github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/payment-order/domain"
	"gorm.io/gorm"
)

// Repository errors
var (
	ErrPaymentOrderNotFound        = errors.New("payment order not found")
	ErrPaymentOrderVersionConflict = errors.New("version conflict: payment order was modified by another transaction")
)

// Repository defines the contract for payment order persistence.
// This interface enables mocking in service-layer tests.
type Repository interface {
	Create(po *domain.PaymentOrder) error
	FindByID(id uuid.UUID) (*domain.PaymentOrder, error)
	FindByIdempotencyKey(key string) (*domain.PaymentOrder, error)
	FindByGatewayReferenceID(gatewayRefID string) (*domain.PaymentOrder, error)
	FindByDebtorAccountID(accountID string) ([]*domain.PaymentOrder, error)
	Update(po *domain.PaymentOrder) error
}

// PaymentOrderRepository provides persistence operations for payment orders
type PaymentOrderRepository struct {
	db *gorm.DB
}

// Compile-time interface compliance check
var _ Repository = (*PaymentOrderRepository)(nil)

// NewPaymentOrderRepository creates a new payment order repository
func NewPaymentOrderRepository(db *gorm.DB) *PaymentOrderRepository {
	return &PaymentOrderRepository{db: db}
}

// Create inserts a new payment order
func (r *PaymentOrderRepository) Create(po *domain.PaymentOrder) error {
	entity := toEntity(po)
	return r.db.Create(entity).Error
}

// FindByID retrieves a payment order by its UUID
func (r *PaymentOrderRepository) FindByID(id uuid.UUID) (*domain.PaymentOrder, error) {
	var entity PaymentOrderEntity
	result := r.db.Where("id = ?", id).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentOrderNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByIdempotencyKey retrieves a payment order by its idempotency key
func (r *PaymentOrderRepository) FindByIdempotencyKey(key string) (*domain.PaymentOrder, error) {
	var entity PaymentOrderEntity
	result := r.db.Where("idempotency_key = ?", key).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentOrderNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByGatewayReferenceID retrieves a payment order by its gateway reference ID
func (r *PaymentOrderRepository) FindByGatewayReferenceID(gatewayRefID string) (*domain.PaymentOrder, error) {
	var entity PaymentOrderEntity
	result := r.db.Where("gateway_reference_id = ?", gatewayRefID).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentOrderNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByDebtorAccountID retrieves all payment orders for a debtor account
func (r *PaymentOrderRepository) FindByDebtorAccountID(accountID string) ([]*domain.PaymentOrder, error) {
	var entities []PaymentOrderEntity
	result := r.db.Where("debtor_account_id = ?", accountID).Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	paymentOrders := make([]*domain.PaymentOrder, 0, len(entities))
	for i := range entities {
		po, err := toDomain(&entities[i])
		if err != nil {
			return nil, err
		}
		paymentOrders = append(paymentOrders, po)
	}

	return paymentOrders, nil
}

// Update updates an existing payment order with optimistic locking
func (r *PaymentOrderRepository) Update(po *domain.PaymentOrder) error {
	entity := toEntity(po)

	// Optimistic locking: use WHERE clause with version check
	result := r.db.Model(&PaymentOrderEntity{}).
		Where("id = ? AND version = ?", entity.ID, po.Version).
		Updates(map[string]interface{}{
			"status":               entity.Status,
			"lien_id":              entity.LienID,
			"gateway_reference_id": entity.GatewayReferenceID,
			"ledger_booking_id":    entity.LedgerBookingID,
			"causation_id":         entity.CausationID,
			"failure_reason":       entity.FailureReason,
			"error_code":           entity.ErrorCode,
			"updated_at":           entity.UpdatedAt,
			"reserved_at":          entity.ReservedAt,
			"executing_at":         entity.ExecutingAt,
			"completed_at":         entity.CompletedAt,
			"failed_at":            entity.FailedAt,
			"cancelled_at":         entity.CancelledAt,
			"reversed_at":          entity.ReversedAt,
			"version":              po.Version + 1,
		})

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return ErrPaymentOrderVersionConflict
	}

	// Update domain model version
	po.Version = po.Version + 1

	return nil
}

// toEntity converts domain model to database entity
func toEntity(po *domain.PaymentOrder) *PaymentOrderEntity {
	return &PaymentOrderEntity{
		ID:                 po.ID,
		DebtorAccountID:    po.DebtorAccountID,
		CreditorReference:  po.CreditorReference,
		AmountCents:        po.Amount.AmountCents(),
		Currency:           po.Amount.Currency(),
		Status:             string(po.Status),
		LienID:             po.LienID,
		GatewayReferenceID: po.GatewayReferenceID,
		LedgerBookingID:    po.LedgerBookingID,
		CorrelationID:      po.CorrelationID,
		CausationID:        po.CausationID,
		IdempotencyKey:     po.IdempotencyKey,
		FailureReason:      po.FailureReason,
		ErrorCode:          po.ErrorCode,
		Version:            po.Version,
		CreatedAt:          po.CreatedAt,
		UpdatedAt:          po.UpdatedAt,
		ReservedAt:         po.ReservedAt,
		ExecutingAt:        po.ExecutingAt,
		CompletedAt:        po.CompletedAt,
		FailedAt:           po.FailedAt,
		CancelledAt:        po.CancelledAt,
		ReversedAt:         po.ReversedAt,
	}
}

// toDomain converts database entity to domain model
func toDomain(entity *PaymentOrderEntity) (*domain.PaymentOrder, error) {
	amount, err := cadomain.NewMoney(entity.Currency, entity.AmountCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create payment order amount from database: %w", err)
	}

	return &domain.PaymentOrder{
		ID:                 entity.ID,
		DebtorAccountID:    entity.DebtorAccountID,
		CreditorReference:  entity.CreditorReference,
		Amount:             amount,
		Status:             domain.PaymentOrderStatus(entity.Status),
		LienID:             entity.LienID,
		GatewayReferenceID: entity.GatewayReferenceID,
		LedgerBookingID:    entity.LedgerBookingID,
		CorrelationID:      entity.CorrelationID,
		CausationID:        entity.CausationID,
		IdempotencyKey:     entity.IdempotencyKey,
		FailureReason:      entity.FailureReason,
		ErrorCode:          entity.ErrorCode,
		Version:            entity.Version,
		CreatedAt:          entity.CreatedAt,
		UpdatedAt:          entity.UpdatedAt,
		ReservedAt:         entity.ReservedAt,
		ExecutingAt:        entity.ExecutingAt,
		CompletedAt:        entity.CompletedAt,
		FailedAt:           entity.FailedAt,
		CancelledAt:        entity.CancelledAt,
		ReversedAt:         entity.ReversedAt,
	}, nil
}
