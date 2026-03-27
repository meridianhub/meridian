package persistence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// BillingRepository errors.
var (
	ErrBillingRunNotFound    = errors.New("billing run not found")
	ErrBillingRunDuplicate   = errors.New("billing run already exists for this tenant and period")
	ErrInvoiceNotFound       = errors.New("invoice not found")
	ErrInvoiceNumberConflict = errors.New("invoice with this number already exists")
	ErrInvalidBillingCursor  = errors.New("invalid billing pagination cursor")
)

// BillingRunFilter specifies criteria for listing billing runs.
type BillingRunFilter struct {
	Statuses []string // Filter by billing run statuses; empty means all.
}

// InvoiceFilter specifies criteria for listing invoices.
type InvoiceFilter struct {
	Statuses     []string // Filter by invoice statuses; empty means all.
	PartyID      string   // Filter by party ID; empty means all.
	BillingRunID string   // Filter by billing run ID; empty means all.
}

// BillingRunPage holds a page of billing run results.
type BillingRunPage struct {
	BillingRuns []*domain.BillingRun
	NextCursor  string
	TotalCount  int64
}

// InvoicePage holds a page of invoice results.
type InvoicePage struct {
	Invoices   []*domain.Invoice
	NextCursor string
	TotalCount int64
}

// EmailAuditEntry represents an email audit record linked to an invoice.
type EmailAuditEntry struct {
	IdempotencyKey string
	TemplateName   string
	ToAddresses    []string
	Status         string
	SentAt         *time.Time
	DeliveredAt    *time.Time
	BounceReason   *string
}

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

	// List methods with cursor-based pagination.
	ListBillingRuns(ctx context.Context, filter BillingRunFilter, pageSize int, pageToken string) (*BillingRunPage, error)
	ListInvoices(ctx context.Context, filter InvoiceFilter, pageSize int, pageToken string) (*InvoicePage, error)
	CountInvoicesByBillingRun(ctx context.Context, billingRunID uuid.UUID) (int64, error)
	SumInvoiceTotalsByBillingRun(ctx context.Context, billingRunID uuid.UUID) (int64, error)

	// Email audit log queries.
	ListEmailsByInvoice(ctx context.Context, invoiceID uuid.UUID) ([]*EmailAuditEntry, error)
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

// encodeBillingCursor encodes a (created_at, id) composite cursor as a base64 page token.
func encodeBillingCursor(createdAt time.Time, id uuid.UUID) string {
	data := createdAt.Format(time.RFC3339Nano) + "|" + id.String()
	return base64.URLEncoding.EncodeToString([]byte(data))
}

// decodeBillingCursor decodes a base64 page token into (created_at, id).
func decodeBillingCursor(token string) (time.Time, uuid.UUID, error) {
	if token == "" {
		return time.Time{}, uuid.Nil, nil
	}

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidBillingCursor
	}

	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, ErrInvalidBillingCursor
	}

	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidBillingCursor
	}

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidBillingCursor
	}

	return createdAt, id, nil
}

// ListBillingRuns returns a paginated list of billing runs with optional status filtering.
func (r *BillingRepositoryImpl) ListBillingRuns(ctx context.Context, filter BillingRunFilter, pageSize int, pageToken string) (*BillingRunPage, error) {
	cursorTime, cursorID, err := decodeBillingCursor(pageToken)
	if err != nil {
		return nil, err
	}

	var page *BillingRunPage
	err = r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&BillingRunEntity{})
		if len(filter.Statuses) > 0 {
			query = query.Where("status IN ?", filter.Statuses)
		}

		var totalCount int64
		if countErr := query.Count(&totalCount).Error; countErr != nil {
			return countErr
		}

		if totalCount == 0 {
			page = &BillingRunPage{BillingRuns: []*domain.BillingRun{}, TotalCount: 0}
			return nil
		}

		// Re-build query for rows (Count mutates the query builder).
		rowQuery := tx.Model(&BillingRunEntity{})
		if len(filter.Statuses) > 0 {
			rowQuery = rowQuery.Where("status IN ?", filter.Statuses)
		}
		if !cursorTime.IsZero() {
			rowQuery = rowQuery.Where(
				"(created_at < ?) OR (created_at = ? AND id < ?)",
				cursorTime, cursorTime, cursorID,
			)
		}

		var entities []BillingRunEntity
		if findErr := rowQuery.Order("created_at DESC, id DESC").Limit(pageSize + 1).Find(&entities).Error; findErr != nil {
			return findErr
		}

		hasMore := len(entities) > pageSize
		if hasMore {
			entities = entities[:pageSize]
		}

		runs := make([]*domain.BillingRun, 0, len(entities))
		for i := range entities {
			runs = append(runs, billingRunToDomain(&entities[i]))
		}

		var nextCursor string
		if hasMore && len(entities) > 0 {
			last := entities[len(entities)-1]
			nextCursor = encodeBillingCursor(last.CreatedAt, last.ID)
		}

		page = &BillingRunPage{
			BillingRuns: runs,
			NextCursor:  nextCursor,
			TotalCount:  totalCount,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// ListInvoices returns a paginated list of invoices with optional filtering.
func (r *BillingRepositoryImpl) ListInvoices(ctx context.Context, filter InvoiceFilter, pageSize int, pageToken string) (*InvoicePage, error) {
	cursorTime, cursorID, err := decodeBillingCursor(pageToken)
	if err != nil {
		return nil, err
	}

	var page *InvoicePage
	err = r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		query := tx.Model(&InvoiceEntity{})
		query = applyInvoiceFilter(query, filter)

		var totalCount int64
		if countErr := query.Count(&totalCount).Error; countErr != nil {
			return countErr
		}

		if totalCount == 0 {
			page = &InvoicePage{Invoices: []*domain.Invoice{}, TotalCount: 0}
			return nil
		}

		rowQuery := tx.Model(&InvoiceEntity{})
		rowQuery = applyInvoiceFilter(rowQuery, filter)
		if !cursorTime.IsZero() {
			rowQuery = rowQuery.Where(
				"(created_at < ?) OR (created_at = ? AND id < ?)",
				cursorTime, cursorTime, cursorID,
			)
		}

		var entities []InvoiceEntity
		if findErr := rowQuery.Order("created_at DESC, id DESC").Limit(pageSize + 1).Find(&entities).Error; findErr != nil {
			return findErr
		}

		hasMore := len(entities) > pageSize
		if hasMore {
			entities = entities[:pageSize]
		}

		invoices := make([]*domain.Invoice, 0, len(entities))
		for i := range entities {
			inv, domainErr := invoiceToDomain(&entities[i])
			if domainErr != nil {
				return domainErr
			}
			invoices = append(invoices, inv)
		}

		var nextCursor string
		if hasMore && len(entities) > 0 {
			last := entities[len(entities)-1]
			nextCursor = encodeBillingCursor(last.CreatedAt, last.ID)
		}

		page = &InvoicePage{
			Invoices:   invoices,
			NextCursor: nextCursor,
			TotalCount: totalCount,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

func applyInvoiceFilter(query *gorm.DB, filter InvoiceFilter) *gorm.DB {
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	if filter.PartyID != "" {
		query = query.Where("party_id = ?", filter.PartyID)
	}
	if filter.BillingRunID != "" {
		query = query.Where("billing_run_id = ?", filter.BillingRunID)
	}
	return query
}

// CountInvoicesByBillingRun returns the number of invoices for a billing run.
func (r *BillingRepositoryImpl) CountInvoicesByBillingRun(ctx context.Context, billingRunID uuid.UUID) (int64, error) {
	var count int64
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&InvoiceEntity{}).Where("billing_run_id = ?", billingRunID).Count(&count).Error
	})
	return count, err
}

// SumInvoiceTotalsByBillingRun returns the total subtotal_cents for all invoices in a billing run.
func (r *BillingRepositoryImpl) SumInvoiceTotalsByBillingRun(ctx context.Context, billingRunID uuid.UUID) (int64, error) {
	var sum int64
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var result struct{ Total *int64 }
		if selectErr := tx.Model(&InvoiceEntity{}).
			Select("COALESCE(SUM(subtotal_cents), 0) as total").
			Where("billing_run_id = ?", billingRunID).
			Scan(&result).Error; selectErr != nil {
			return selectErr
		}
		if result.Total != nil {
			sum = *result.Total
		}
		return nil
	})
	return sum, err
}

// ListEmailsByInvoice returns email audit entries for the given invoice.
// It finds outbox entries whose idempotency_key starts with "invoice-{invoiceID}"
// to build the email audit trail.
func (r *BillingRepositoryImpl) ListEmailsByInvoice(ctx context.Context, invoiceID uuid.UUID) ([]*EmailAuditEntry, error) {
	pattern := "invoice-" + invoiceID.String() + "%"
	var entries []*EmailAuditEntry

	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var outboxEntities []email.OutboxEntity
		if findErr := tx.Where("idempotency_key LIKE ?", pattern).
			Order("created_at DESC").
			Find(&outboxEntities).Error; findErr != nil {
			return findErr
		}

		entries = make([]*EmailAuditEntry, 0, len(outboxEntities))
		for i := range outboxEntities {
			e := &outboxEntities[i]
			entry := &EmailAuditEntry{
				IdempotencyKey: e.IdempotencyKey,
				TemplateName:   e.TemplateName,
				ToAddresses:    []string(e.ToAddresses),
				Status:         e.Status,
			}
			entries = append(entries, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}
