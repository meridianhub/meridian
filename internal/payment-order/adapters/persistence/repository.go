package persistence

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	cadomain "github.com/meridianhub/meridian/internal/current-account/domain"
	"github.com/meridianhub/meridian/internal/payment-order/domain"
	"gorm.io/gorm"
)

// Repository errors
var (
	ErrPaymentOrderNotFound           = errors.New("payment order not found")
	ErrPaymentOrderVersionConflict    = errors.New("version conflict: payment order was modified by another transaction")
	ErrIdempotencyKeyConflict         = errors.New("payment order with this idempotency key already exists")
	ErrInvalidCursor                  = errors.New("invalid pagination cursor")
	errUniqueConstraintIdempotencyKey = "payment_orders_idempotency_key_key" // PostgreSQL constraint name
)

// Cursor represents a pagination cursor for cursor-based pagination.
// It uses created_at + id as a composite cursor to handle ties (items with same created_at).
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// EncodeCursor encodes a cursor to a base64 string for use as an opaque page token.
// Format: RFC3339Nano timestamp + "|" + UUID
func EncodeCursor(c Cursor) string {
	data := c.CreatedAt.Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.URLEncoding.EncodeToString([]byte(data))
}

// DecodeCursor decodes a base64 page token back to a Cursor.
// Returns ErrInvalidCursor if the token is malformed.
func DecodeCursor(token string) (Cursor, error) {
	if token == "" {
		return Cursor{}, nil
	}

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}

	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return Cursor{}, ErrInvalidCursor
	}

	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}

	return Cursor{CreatedAt: createdAt, ID: id}, nil
}

// PaginatedResult holds paginated query results
type PaginatedResult struct {
	PaymentOrders []*domain.PaymentOrder
	TotalCount    int64
	HasMore       bool
	// NextCursor is the cursor for fetching the next page (empty if no more results)
	NextCursor string
}

// Repository defines the contract for payment order persistence.
// This interface enables mocking in service-layer tests.
// All methods accept context.Context as the first parameter to enable
// proper request lifecycle management, timeout handling, and cancellation propagation.
type Repository interface {
	Create(ctx context.Context, po *domain.PaymentOrder) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.PaymentOrder, error)
	FindByIdempotencyKey(ctx context.Context, key string) (*domain.PaymentOrder, error)
	FindByGatewayReferenceID(ctx context.Context, gatewayRefID string) (*domain.PaymentOrder, error)
	FindByDebtorAccountID(ctx context.Context, accountID string) ([]*domain.PaymentOrder, error)
	// FindByDebtorAccountIDWithCursor retrieves payment orders using cursor-based pagination.
	// Pass an empty cursor for the first page. Results are ordered by created_at DESC, id DESC.
	FindByDebtorAccountIDWithCursor(ctx context.Context, accountID string, limit int, cursor Cursor) (*PaginatedResult, error)
	Update(ctx context.Context, po *domain.PaymentOrder) error
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

// Create inserts a new payment order.
// Returns ErrIdempotencyKeyConflict if a payment order with the same idempotency key exists.
func (r *PaymentOrderRepository) Create(ctx context.Context, po *domain.PaymentOrder) error {
	entity := toEntity(po)
	err := r.db.WithContext(ctx).Create(entity).Error
	if err != nil && strings.Contains(err.Error(), errUniqueConstraintIdempotencyKey) {
		return ErrIdempotencyKeyConflict
	}
	return err
}

// FindByID retrieves a payment order by its UUID
func (r *PaymentOrderRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.PaymentOrder, error) {
	var entity PaymentOrderEntity
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentOrderNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByIdempotencyKey retrieves a payment order by its idempotency key
func (r *PaymentOrderRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.PaymentOrder, error) {
	var entity PaymentOrderEntity
	result := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentOrderNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByGatewayReferenceID retrieves a payment order by its gateway reference ID
func (r *PaymentOrderRepository) FindByGatewayReferenceID(ctx context.Context, gatewayRefID string) (*domain.PaymentOrder, error) {
	var entity PaymentOrderEntity
	result := r.db.WithContext(ctx).Where("gateway_reference_id = ?", gatewayRefID).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrPaymentOrderNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// FindByDebtorAccountID retrieves all payment orders for a debtor account
func (r *PaymentOrderRepository) FindByDebtorAccountID(ctx context.Context, accountID string) ([]*domain.PaymentOrder, error) {
	var entities []PaymentOrderEntity
	result := r.db.WithContext(ctx).Where("debtor_account_id = ?", accountID).Find(&entities)

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

// FindByDebtorAccountIDWithCursor retrieves payment orders for a debtor account with cursor-based pagination.
// This provides consistent results even when items are inserted/deleted during pagination.
// Results are ordered by created_at DESC, id DESC (newest first) for deterministic ordering.
//
// The cursor uses (created_at, id) as a composite key to handle ties when multiple records
// have the same created_at timestamp. The query uses:
//
//	WHERE (created_at < cursor_time) OR (created_at = cursor_time AND id < cursor_id)
//
// This ensures stable pagination even with concurrent inserts.
func (r *PaymentOrderRepository) FindByDebtorAccountIDWithCursor(ctx context.Context, accountID string, limit int, cursor Cursor) (*PaginatedResult, error) {
	// Get total count (for UI purposes - this is still useful for showing "X of Y" in pagination)
	var totalCount int64
	countResult := r.db.WithContext(ctx).Model(&PaymentOrderEntity{}).
		Where("debtor_account_id = ?", accountID).
		Count(&totalCount)

	if countResult.Error != nil {
		return nil, countResult.Error
	}

	// Return early if no results
	if totalCount == 0 {
		return &PaginatedResult{
			PaymentOrders: []*domain.PaymentOrder{},
			TotalCount:    0,
			HasMore:       false,
			NextCursor:    "",
		}, nil
	}

	// Build query with cursor-based pagination
	// Request one extra to determine if there are more results
	query := r.db.WithContext(ctx).Where("debtor_account_id = ?", accountID)

	// Apply cursor condition if provided (not first page)
	if !cursor.CreatedAt.IsZero() {
		// Use composite cursor: (created_at < cursor_time) OR (created_at = cursor_time AND id < cursor_id)
		// This handles ties when multiple records have the same created_at
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt, cursor.CreatedAt, cursor.ID,
		)
	}

	var entities []PaymentOrderEntity
	result := query.
		Order("created_at DESC, id DESC").
		Limit(limit + 1). // Fetch one extra to check for more
		Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	// Determine if there are more results
	hasMore := len(entities) > limit
	if hasMore {
		entities = entities[:limit] // Trim to requested limit
	}

	paymentOrders := make([]*domain.PaymentOrder, 0, len(entities))
	for i := range entities {
		po, err := toDomain(&entities[i])
		if err != nil {
			return nil, err
		}
		paymentOrders = append(paymentOrders, po)
	}

	// Build next cursor from the last item if there are more results
	var nextCursor string
	if hasMore && len(paymentOrders) > 0 {
		lastPO := paymentOrders[len(paymentOrders)-1]
		nextCursor = EncodeCursor(Cursor{
			CreatedAt: lastPO.CreatedAt,
			ID:        lastPO.ID,
		})
	}

	return &PaginatedResult{
		PaymentOrders: paymentOrders,
		TotalCount:    totalCount,
		HasMore:       hasMore,
		NextCursor:    nextCursor,
	}, nil
}

// Update updates an existing payment order with optimistic locking
func (r *PaymentOrderRepository) Update(ctx context.Context, po *domain.PaymentOrder) error {
	entity := toEntity(po)

	// Optimistic locking: use WHERE clause with version check
	result := r.db.WithContext(ctx).Model(&PaymentOrderEntity{}).
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
