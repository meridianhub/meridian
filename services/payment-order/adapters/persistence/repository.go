package persistence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/db"
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
func NewPaymentOrderRepository(gormDB *gorm.DB) *PaymentOrderRepository {
	return &PaymentOrderRepository{db: gormDB}
}

// withTenantTransaction executes the given function with tenant scoping.
// The system is always in multi-tenant mode, so this wraps the function in a transaction
// and sets the search_path. This helper reduces code duplication across repository methods.
func (r *PaymentOrderRepository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Create inserts a new payment order.
// Returns ErrIdempotencyKeyConflict if a payment order with the same idempotency key exists.
// The context must contain the tenant ID for schema routing.
func (r *PaymentOrderRepository) Create(ctx context.Context, po *domain.PaymentOrder) error {
	entity := toEntity(po)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		err := tx.Create(entity).Error
		if err != nil && strings.Contains(err.Error(), errUniqueConstraintIdempotencyKey) {
			return ErrIdempotencyKeyConflict
		}
		return err
	})
}

// FindByID retrieves a payment order by its UUID.
// The context must contain the tenant ID for schema routing.
func (r *PaymentOrderRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.PaymentOrder, error) {
	var paymentOrder *domain.PaymentOrder
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PaymentOrderEntity
		result := tx.Where("id = ?", id).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPaymentOrderNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		var domainErr error
		paymentOrder, domainErr = toDomain(&entity)
		return domainErr
	})
	if err != nil {
		return nil, err
	}
	return paymentOrder, nil
}

// FindByIdempotencyKey retrieves a payment order by its idempotency key.
// The context must contain the tenant ID for schema routing.
func (r *PaymentOrderRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.PaymentOrder, error) {
	var paymentOrder *domain.PaymentOrder
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PaymentOrderEntity
		result := tx.Where("idempotency_key = ?", key).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPaymentOrderNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		var domainErr error
		paymentOrder, domainErr = toDomain(&entity)
		return domainErr
	})
	if err != nil {
		return nil, err
	}
	return paymentOrder, nil
}

// FindByGatewayReferenceID retrieves a payment order by its gateway reference ID.
// The context must contain the tenant ID for schema routing.
func (r *PaymentOrderRepository) FindByGatewayReferenceID(ctx context.Context, gatewayRefID string) (*domain.PaymentOrder, error) {
	var paymentOrder *domain.PaymentOrder
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity PaymentOrderEntity
		result := tx.Where("gateway_reference_id = ?", gatewayRefID).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrPaymentOrderNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		var domainErr error
		paymentOrder, domainErr = toDomain(&entity)
		return domainErr
	})
	if err != nil {
		return nil, err
	}
	return paymentOrder, nil
}

// FindByDebtorAccountID retrieves all payment orders for a debtor account.
// The context must contain the tenant ID for schema routing.
func (r *PaymentOrderRepository) FindByDebtorAccountID(ctx context.Context, accountID string) ([]*domain.PaymentOrder, error) {
	var paymentOrders []*domain.PaymentOrder
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []PaymentOrderEntity
		result := tx.Where("debtor_account_id = ?", accountID).Find(&entities)

		if result.Error != nil {
			return result.Error
		}

		paymentOrders = make([]*domain.PaymentOrder, 0, len(entities))
		for i := range entities {
			po, err := toDomain(&entities[i])
			if err != nil {
				return err
			}
			paymentOrders = append(paymentOrders, po)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paymentOrders, nil
}

// FindByDebtorAccountIDWithCursor retrieves payment orders for a debtor account with cursor-based pagination.
// This provides consistent results even when items are inserted/deleted during pagination.
// Results are ordered by created_at DESC, id DESC (newest first) for deterministic ordering.
// The context must contain the tenant ID for schema routing.
//
// The cursor uses (created_at, id) as a composite key to handle ties when multiple records
// have the same created_at timestamp. The query uses:
//
//	WHERE (created_at < cursor_time) OR (created_at = cursor_time AND id < cursor_id)
//
// This ensures stable pagination even with concurrent inserts.
func (r *PaymentOrderRepository) FindByDebtorAccountIDWithCursor(ctx context.Context, accountID string, limit int, cursor Cursor) (*PaginatedResult, error) {
	var paginatedResult *PaginatedResult
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var totalCount int64
		countResult := tx.Model(&PaymentOrderEntity{}).
			Where("debtor_account_id = ?", accountID).
			Count(&totalCount)

		if countResult.Error != nil {
			return countResult.Error
		}

		if totalCount == 0 {
			paginatedResult = &PaginatedResult{
				PaymentOrders: []*domain.PaymentOrder{},
				TotalCount:    0,
				HasMore:       false,
				NextCursor:    "",
			}
			return nil
		}

		entities, err := queryCursorPage(tx, accountID, limit, cursor)
		if err != nil {
			return err
		}

		hasMore := len(entities) > limit
		if hasMore {
			entities = entities[:limit]
		}

		paymentOrders, err := mapEntitiesToDomain(entities)
		if err != nil {
			return err
		}

		paginatedResult = &PaginatedResult{
			PaymentOrders: paymentOrders,
			TotalCount:    totalCount,
			HasMore:       hasMore,
			NextCursor:    buildNextCursor(paymentOrders, hasMore),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paginatedResult, nil
}

// queryCursorPage executes the cursor-based pagination query, fetching limit+1 rows to detect more results.
func queryCursorPage(tx *gorm.DB, accountID string, limit int, cursor Cursor) ([]PaymentOrderEntity, error) {
	query := tx.Where("debtor_account_id = ?", accountID)

	if !cursor.CreatedAt.IsZero() {
		query = query.Where(
			"(created_at < ?) OR (created_at = ? AND id < ?)",
			cursor.CreatedAt, cursor.CreatedAt, cursor.ID,
		)
	}

	var entities []PaymentOrderEntity
	result := query.
		Order("created_at DESC, id DESC").
		Limit(limit + 1).
		Find(&entities)

	return entities, result.Error
}

// mapEntitiesToDomain converts a slice of PaymentOrderEntity to domain models.
func mapEntitiesToDomain(entities []PaymentOrderEntity) ([]*domain.PaymentOrder, error) {
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

// buildNextCursor encodes a cursor from the last payment order if there are more results.
func buildNextCursor(paymentOrders []*domain.PaymentOrder, hasMore bool) string {
	if !hasMore || len(paymentOrders) == 0 {
		return ""
	}
	lastPO := paymentOrders[len(paymentOrders)-1]
	return EncodeCursor(Cursor{
		CreatedAt: lastPO.CreatedAt,
		ID:        lastPO.ID,
	})
}

// Update updates an existing payment order with optimistic locking.
// The context must contain the tenant ID for schema routing.
// Records an audit entry capturing the old and new state for compliance tracking.
func (r *PaymentOrderRepository) Update(ctx context.Context, po *domain.PaymentOrder) error {
	newEntity := toEntity(po)

	//nolint:contextcheck // Context accessed from tx.Statement.Context per GORM audit hook convention
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		// Fetch old entity state for audit trail BEFORE update
		var oldEntity PaymentOrderEntity
		if err := tx.Where("id = ?", newEntity.ID).First(&oldEntity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPaymentOrderNotFound
			}
			return err
		}

		// Optimistic locking: use WHERE clause with version check
		result := tx.Model(&PaymentOrderEntity{}).
			Where("id = ? AND version = ?", newEntity.ID, po.Version).
			Updates(map[string]interface{}{
				"status":                  newEntity.Status,
				"lien_id":                 newEntity.LienID,
				"gateway_reference_id":    newEntity.GatewayReferenceID,
				"ledger_booking_id":       newEntity.LedgerBookingID,
				"causation_id":            newEntity.CausationID,
				"failure_reason":          newEntity.FailureReason,
				"error_code":              newEntity.ErrorCode,
				"lien_execution_status":   newEntity.LienExecutionStatus,
				"lien_execution_attempts": newEntity.LienExecutionAttempts,
				"lien_execution_error":    newEntity.LienExecutionError,
				"instrument_code":         newEntity.InstrumentCode,
				"payment_attributes":      newEntity.PaymentAttributes,
				"bucket_id":               newEntity.BucketID,
				"updated_at":              newEntity.UpdatedAt,
				"reserved_at":             newEntity.ReservedAt,
				"executing_at":            newEntity.ExecutingAt,
				"completed_at":            newEntity.CompletedAt,
				"failed_at":               newEntity.FailedAt,
				"cancelled_at":            newEntity.CancelledAt,
				"reversed_at":             newEntity.ReversedAt,
				"version":                 po.Version + 1,
			})

		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return ErrPaymentOrderVersionConflict
		}

		// Record audit entry for the update (explicit audit for Map-based Updates)
		// Update newEntity version to match what we just wrote to DB
		newEntity.Version = po.Version + 1
		return audit.RecordUpdateManual(tx, oldEntity, *newEntity)
	})
	if err != nil {
		return err
	}

	// Update domain model version
	po.Version++

	return nil
}

// toEntity converts domain model to database entity
func toEntity(po *domain.PaymentOrder) *PaymentOrderEntity {
	// domain.ToMinorUnits safely converts Money to minor units (cents).
	// The domain layer validates amounts before persistence, ensuring valid values.
	entity := &PaymentOrderEntity{
		ID:                    po.ID,
		DebtorAccountID:       po.DebtorAccountID,
		CreditorReference:     po.CreditorReference,
		AmountCents:           domain.ToMinorUnits(po.Amount),
		Currency:              domain.CurrencyCode(po.Amount),
		Status:                string(po.Status),
		LienID:                po.LienID,
		GatewayReferenceID:    po.GatewayReferenceID,
		LedgerBookingID:       po.LedgerBookingID,
		CorrelationID:         po.CorrelationID,
		CausationID:           po.CausationID,
		IdempotencyKey:        po.IdempotencyKey,
		FailureReason:         po.FailureReason,
		ErrorCode:             po.ErrorCode,
		LienExecutionAttempts: po.LienExecutionAttempts,
		InstrumentCode:        po.InstrumentCode,
		Version:               po.Version,
		CreatedAt:             po.CreatedAt,
		UpdatedAt:             po.UpdatedAt,
		ReservedAt:            po.ReservedAt,
		ExecutingAt:           po.ExecutingAt,
		CompletedAt:           po.CompletedAt,
		FailedAt:              po.FailedAt,
		CancelledAt:           po.CancelledAt,
		ReversedAt:            po.ReversedAt,
	}

	// Handle nullable string fields
	if po.LienExecutionStatus != "" {
		status := string(po.LienExecutionStatus)
		entity.LienExecutionStatus = &status
	}
	if po.LienExecutionError != "" {
		entity.LienExecutionError = &po.LienExecutionError
	}
	if po.BucketID != "" {
		entity.BucketID = &po.BucketID
	}

	// Serialize PaymentAttributes to JSON (NULL when empty to satisfy JSONB constraint)
	if len(po.PaymentAttributes) > 0 {
		attrs, err := json.Marshal(po.PaymentAttributes)
		if err != nil {
			// Log the error - this shouldn't happen with map[string]string
			// but indicates a programming error if it does
			slog.Error("failed to marshal payment attributes",
				"error", err,
				"payment_order_id", po.ID.String())
		} else {
			attrsStr := string(attrs)
			entity.PaymentAttributes = &attrsStr
		}
	}

	return entity
}

// toDomain converts database entity to domain model
func toDomain(entity *PaymentOrderEntity) (*domain.PaymentOrder, error) {
	amount, err := domain.NewMoney(entity.Currency, entity.AmountCents)
	if err != nil {
		return nil, fmt.Errorf("failed to create payment order amount from database: %w", err)
	}

	// Handle nullable string fields from database
	var lienExecutionStatus domain.LienExecutionStatus
	if entity.LienExecutionStatus != nil {
		lienExecutionStatus = domain.LienExecutionStatus(*entity.LienExecutionStatus)
	}

	var lienExecutionError string
	if entity.LienExecutionError != nil {
		lienExecutionError = *entity.LienExecutionError
	}

	var bucketID string
	if entity.BucketID != nil {
		bucketID = *entity.BucketID
	}

	// Deserialize PaymentAttributes from JSON
	var paymentAttributes map[string]string
	if entity.PaymentAttributes != nil && *entity.PaymentAttributes != "" {
		if err := json.Unmarshal([]byte(*entity.PaymentAttributes), &paymentAttributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payment attributes: %w", err)
		}
	}

	return &domain.PaymentOrder{
		ID:                    entity.ID,
		DebtorAccountID:       entity.DebtorAccountID,
		CreditorReference:     entity.CreditorReference,
		Amount:                amount,
		Status:                domain.PaymentOrderStatus(entity.Status),
		LienID:                entity.LienID,
		GatewayReferenceID:    entity.GatewayReferenceID,
		LedgerBookingID:       entity.LedgerBookingID,
		CorrelationID:         entity.CorrelationID,
		CausationID:           entity.CausationID,
		IdempotencyKey:        entity.IdempotencyKey,
		FailureReason:         entity.FailureReason,
		ErrorCode:             entity.ErrorCode,
		LienExecutionStatus:   lienExecutionStatus,
		LienExecutionAttempts: entity.LienExecutionAttempts,
		LienExecutionError:    lienExecutionError,
		InstrumentCode:        entity.InstrumentCode,
		PaymentAttributes:     paymentAttributes,
		BucketID:              bucketID,
		Version:               entity.Version,
		CreatedAt:             entity.CreatedAt,
		UpdatedAt:             entity.UpdatedAt,
		ReservedAt:            entity.ReservedAt,
		ExecutingAt:           entity.ExecutingAt,
		CompletedAt:           entity.CompletedAt,
		FailedAt:              entity.FailedAt,
		CancelledAt:           entity.CancelledAt,
		ReversedAt:            entity.ReversedAt,
	}, nil
}
