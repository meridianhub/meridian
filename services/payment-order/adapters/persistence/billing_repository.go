package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// BillingRepository errors.
var (
	ErrBillingRunNotFound    = errors.New("billing run not found")
	ErrBillingRunDuplicate   = errors.New("billing run already exists for this tenant and period")
	ErrInvoiceNotFound       = errors.New("invoice not found")
	ErrInvoiceNumberConflict = errors.New("invoice with this number already exists")
)

// BillingRepository defines the contract for billing persistence.
type BillingRepository interface {
	CreateBillingRun(ctx context.Context, run *domain.BillingRun) error
	FindBillingRunByID(ctx context.Context, id uuid.UUID) (*domain.BillingRun, error)
	FindBillingRunByTenantAndPeriod(ctx context.Context, tenantID string, cycleStart, cycleEnd time.Time) (*domain.BillingRun, error)
	UpdateBillingRun(ctx context.Context, run *domain.BillingRun) error
	CreateInvoice(ctx context.Context, inv *domain.Invoice) error
	FindInvoiceByID(ctx context.Context, id uuid.UUID) (*domain.Invoice, error)
	FindInvoicesByBillingRunID(ctx context.Context, billingRunID uuid.UUID) ([]*domain.Invoice, error)
	UpdateInvoice(ctx context.Context, inv *domain.Invoice) error
}

// BillingRepositoryImpl provides persistence operations for billing entities.
type BillingRepositoryImpl struct {
	db *gorm.DB
}

// Compile-time interface compliance check.
var _ BillingRepository = (*BillingRepositoryImpl)(nil)

// NewBillingRepository creates a new billing repository.
func NewBillingRepository(gormDB *gorm.DB) *BillingRepositoryImpl {
	return &BillingRepositoryImpl{db: gormDB}
}

func (r *BillingRepositoryImpl) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// CreateBillingRun inserts a new billing run.
func (r *BillingRepositoryImpl) CreateBillingRun(ctx context.Context, run *domain.BillingRun) error {
	entity := billingRunToEntity(run)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		err := tx.Create(entity).Error
		if err != nil && isDuplicateKeyError(err) {
			return ErrBillingRunDuplicate
		}
		return err
	})
}

// FindBillingRunByID retrieves a billing run by ID.
func (r *BillingRepositoryImpl) FindBillingRunByID(ctx context.Context, id uuid.UUID) (*domain.BillingRun, error) {
	var result *domain.BillingRun
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity BillingRunEntity
		if err := tx.Where("id = ?", id).First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBillingRunNotFound
			}
			return err
		}
		result = billingRunToDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// FindBillingRunByTenantAndPeriod finds a billing run by tenant and period for idempotency checking.
func (r *BillingRepositoryImpl) FindBillingRunByTenantAndPeriod(ctx context.Context, tenantID string, cycleStart, cycleEnd time.Time) (*domain.BillingRun, error) {
	var result *domain.BillingRun
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity BillingRunEntity
		if err := tx.Where("tenant_id = ? AND cycle_start = ? AND cycle_end = ?", tenantID, cycleStart, cycleEnd).First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBillingRunNotFound
			}
			return err
		}
		result = billingRunToDomain(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateBillingRun updates an existing billing run.
func (r *BillingRepositoryImpl) UpdateBillingRun(ctx context.Context, run *domain.BillingRun) error {
	entity := billingRunToEntity(run)
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&BillingRunEntity{}).Where("id = ?", entity.ID).Updates(map[string]interface{}{
			"status":         entity.Status,
			"dunning_level":  entity.DunningLevel,
			"failure_reason": entity.FailureReason,
			"last_retry_at":  entity.LastRetryAt,
			"updated_at":     entity.UpdatedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrBillingRunNotFound
		}
		return nil
	})
}

// CreateInvoice inserts a new invoice.
func (r *BillingRepositoryImpl) CreateInvoice(ctx context.Context, inv *domain.Invoice) error {
	entity, err := invoiceToEntity(inv)
	if err != nil {
		return fmt.Errorf("failed to serialize invoice: %w", err)
	}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		createErr := tx.Create(entity).Error
		if createErr != nil && isDuplicateKeyError(createErr) {
			return ErrInvoiceNumberConflict
		}
		return createErr
	})
}

// FindInvoiceByID retrieves an invoice by ID.
func (r *BillingRepositoryImpl) FindInvoiceByID(ctx context.Context, id uuid.UUID) (*domain.Invoice, error) {
	var result *domain.Invoice
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity InvoiceEntity
		if err := tx.Where("id = ?", id).First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvoiceNotFound
			}
			return err
		}
		var domainErr error
		result, domainErr = invoiceToDomain(&entity)
		return domainErr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// FindInvoicesByBillingRunID retrieves all invoices for a billing run.
func (r *BillingRepositoryImpl) FindInvoicesByBillingRunID(ctx context.Context, billingRunID uuid.UUID) ([]*domain.Invoice, error) {
	var results []*domain.Invoice
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []InvoiceEntity
		if err := tx.Where("billing_run_id = ?", billingRunID).Find(&entities).Error; err != nil {
			return err
		}
		results = make([]*domain.Invoice, 0, len(entities))
		for i := range entities {
			inv, err := invoiceToDomain(&entities[i])
			if err != nil {
				return err
			}
			results = append(results, inv)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// UpdateInvoice updates an existing invoice.
func (r *BillingRepositoryImpl) UpdateInvoice(ctx context.Context, inv *domain.Invoice) error {
	entity, err := invoiceToEntity(inv)
	if err != nil {
		return fmt.Errorf("failed to serialize invoice: %w", err)
	}
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&InvoiceEntity{}).Where("id = ?", entity.ID).Updates(map[string]interface{}{
			"status":           entity.Status,
			"payment_order_id": entity.PaymentOrderID,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrInvoiceNotFound
		}
		return nil
	})
}

// isDuplicateKeyError checks if a GORM error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// CockroachDB / PostgreSQL duplicate key patterns
	return strings.Contains(errStr, "duplicate key") || strings.Contains(errStr, "SQLSTATE 23505")
}

func billingRunToEntity(run *domain.BillingRun) *BillingRunEntity {
	entity := &BillingRunEntity{
		ID:           run.ID,
		TenantID:     run.TenantID,
		CycleStart:   run.CycleStart,
		CycleEnd:     run.CycleEnd,
		Status:       string(run.Status),
		DunningLevel: run.DunningLevel,
		LastRetryAt:  run.LastRetryAt,
		CreatedAt:    run.CreatedAt,
		UpdatedAt:    run.UpdatedAt,
	}
	if run.FailureReason != "" {
		entity.FailureReason = &run.FailureReason
	}
	return entity
}

func billingRunToDomain(entity *BillingRunEntity) *domain.BillingRun {
	run := &domain.BillingRun{
		ID:           entity.ID,
		TenantID:     entity.TenantID,
		CycleStart:   entity.CycleStart,
		CycleEnd:     entity.CycleEnd,
		Status:       domain.BillingRunStatus(entity.Status),
		DunningLevel: entity.DunningLevel,
		LastRetryAt:  entity.LastRetryAt,
		CreatedAt:    entity.CreatedAt,
		UpdatedAt:    entity.UpdatedAt,
	}
	if entity.FailureReason != nil {
		run.FailureReason = *entity.FailureReason
	}
	return run
}

func invoiceToEntity(inv *domain.Invoice) (*InvoiceEntity, error) {
	lineItemsJSON, err := json.Marshal(inv.LineItems)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal line items: %w", err)
	}

	return &InvoiceEntity{
		ID:             inv.ID,
		BillingRunID:   inv.BillingRunID,
		PartyID:        inv.PartyID,
		AccountID:      inv.AccountID,
		InvoiceNumber:  inv.InvoiceNumber,
		PeriodStart:    inv.PeriodStart,
		PeriodEnd:      inv.PeriodEnd,
		LineItems:      string(lineItemsJSON),
		SubtotalCents:  inv.SubtotalCents,
		Currency:       inv.Currency,
		Status:         string(inv.Status),
		PaymentOrderID: inv.PaymentOrderID,
		CreatedAt:      inv.CreatedAt,
	}, nil
}

func invoiceToDomain(entity *InvoiceEntity) (*domain.Invoice, error) {
	var lineItems []domain.InvoiceLineItem
	if err := json.Unmarshal([]byte(entity.LineItems), &lineItems); err != nil {
		return nil, fmt.Errorf("failed to unmarshal line items: %w", err)
	}

	return &domain.Invoice{
		ID:             entity.ID,
		BillingRunID:   entity.BillingRunID,
		PartyID:        entity.PartyID,
		AccountID:      entity.AccountID,
		InvoiceNumber:  entity.InvoiceNumber,
		PeriodStart:    entity.PeriodStart,
		PeriodEnd:      entity.PeriodEnd,
		LineItems:      lineItems,
		SubtotalCents:  entity.SubtotalCents,
		Currency:       entity.Currency,
		Status:         domain.InvoiceStatus(entity.Status),
		PaymentOrderID: entity.PaymentOrderID,
		CreatedAt:      entity.CreatedAt,
	}, nil
}
