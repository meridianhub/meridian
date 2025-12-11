package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"gorm.io/gorm"
)

// Repository errors.
var (
	ErrTenantNotFound  = errors.New("tenant not found")
	ErrTenantExists    = errors.New("tenant already exists")
	ErrVersionConflict = errors.New("version conflict: tenant was modified by another transaction")
	ErrSubdomainTaken  = errors.New("subdomain already taken by another tenant")
)

// Repository provides persistence operations for tenants.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new tenant repository.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// DB returns the underlying database connection for transaction support.
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// WithTx returns a new Repository that uses the provided transaction.
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// Create registers a new tenant (BIAN: Initiate).
func (r *Repository) Create(ctx context.Context, tenant *domain.Tenant) error {
	entity := toEntity(tenant)

	if err := r.db.WithContext(ctx).Create(&entity).Error; err != nil {
		if isDuplicateKeyError(err) {
			if strings.Contains(err.Error(), "subdomain") {
				return ErrSubdomainTaken
			}
			return ErrTenantExists
		}
		return err
	}

	// Update domain model with created timestamp
	tenant.CreatedAt = entity.CreatedAt
	tenant.Version = entity.Version

	return nil
}

// GetByID retrieves a tenant by ID (BIAN: Retrieve).
func (r *Repository) GetByID(ctx context.Context, id organization.OrganizationID) (*domain.Tenant, error) {
	var entity TenantEntity
	result := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrTenantNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// IsActive checks if a tenant exists and is active.
// This is optimized for validation middleware - returns only what's needed.
func (r *Repository) IsActive(ctx context.Context, id organization.OrganizationID) (bool, error) {
	var status string
	result := r.db.WithContext(ctx).
		Model(&TenantEntity{}).
		Select("status").
		Where("id = ?", id.String()).
		Take(&status)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false, ErrTenantNotFound
	}

	if result.Error != nil {
		return false, result.Error
	}

	return status == string(domain.StatusActive), nil
}

// UpdateStatus changes the tenant status (BIAN: Update).
func (r *Repository) UpdateStatus(ctx context.Context, id organization.OrganizationID, status domain.Status, currentVersion int) (*domain.Tenant, error) {
	updates := map[string]interface{}{
		"status":  status,
		"version": currentVersion + 1,
	}

	// Set deprovisioned_at timestamp when deprovisioning
	if status == domain.StatusDeprovisioned {
		now := time.Now()
		updates["deprovisioned_at"] = &now
	}

	result := r.db.WithContext(ctx).
		Model(&TenantEntity{}).
		Where("id = ? AND version = ?", id.String(), currentVersion).
		Updates(updates)

	if result.Error != nil {
		return nil, result.Error
	}

	if result.RowsAffected == 0 {
		// Check if tenant exists
		var count int64
		r.db.WithContext(ctx).
			Model(&TenantEntity{}).
			Where("id = ?", id.String()).
			Count(&count)

		if count == 0 {
			return nil, ErrTenantNotFound
		}
		return nil, ErrVersionConflict
	}

	// Fetch and return the updated tenant
	return r.GetByID(ctx, id)
}

// List returns tenants with optional status filter (BIAN: Control).
func (r *Repository) List(ctx context.Context, statusFilter *domain.Status, pageSize int, pageToken string) ([]*domain.Tenant, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	query := r.db.WithContext(ctx).Model(&TenantEntity{})

	// Apply status filter if provided
	if statusFilter != nil {
		query = query.Where("status = ?", *statusFilter)
	}

	// Apply pagination token (cursor-based using created_at)
	if pageToken != "" {
		// Page token is the created_at timestamp of the last item
		query = query.Where("created_at < ?", pageToken)
	}

	// Order by created_at descending (newest first) and limit
	query = query.Order("created_at DESC").Limit(pageSize + 1) // +1 to check if there's a next page

	var entities []TenantEntity
	if err := query.Find(&entities).Error; err != nil {
		return nil, "", err
	}

	// Determine next page token
	var nextPageToken string
	if len(entities) > pageSize {
		// There's a next page
		entities = entities[:pageSize]
		nextPageToken = entities[pageSize-1].CreatedAt.Format(time.RFC3339Nano)
	}

	// Convert to domain models
	tenants := make([]*domain.Tenant, 0, len(entities))
	for i := range entities {
		tenant, err := toDomain(&entities[i])
		if err != nil {
			return nil, "", err
		}
		tenants = append(tenants, tenant)
	}

	return tenants, nextPageToken, nil
}

// GetAll returns all tenants (for cache initialization).
func (r *Repository) GetAll(ctx context.Context) ([]*domain.Tenant, error) {
	var entities []TenantEntity
	if err := r.db.WithContext(ctx).Find(&entities).Error; err != nil {
		return nil, err
	}

	tenants := make([]*domain.Tenant, 0, len(entities))
	for i := range entities {
		tenant, err := toDomain(&entities[i])
		if err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}

	return tenants, nil
}

// Ping checks database connectivity.
func (r *Repository) Ping(ctx context.Context) error {
	var result int
	return r.db.WithContext(ctx).Raw("SELECT 1").Scan(&result).Error
}

// toEntity converts domain model to database entity.
func toEntity(tenant *domain.Tenant) *TenantEntity {
	entity := &TenantEntity{
		ID:              tenant.ID.String(),
		DisplayName:     tenant.DisplayName,
		SettlementAsset: tenant.SettlementAsset,
		Status:          string(tenant.Status),
		CreatedAt:       tenant.CreatedAt,
		DeprovisionedAt: tenant.DeprovisionedAt,
		Metadata:        JSONMap(tenant.Metadata),
		Version:         tenant.Version,
	}

	if tenant.Subdomain != "" {
		entity.Subdomain = &tenant.Subdomain
	}

	return entity
}

// toDomain converts database entity to domain model.
func toDomain(entity *TenantEntity) (*domain.Tenant, error) {
	tenantID, err := organization.NewOrganizationID(entity.ID)
	if err != nil {
		return nil, err
	}

	tenant := &domain.Tenant{
		ID:              tenantID,
		DisplayName:     entity.DisplayName,
		SettlementAsset: entity.SettlementAsset,
		Status:          domain.Status(entity.Status),
		CreatedAt:       entity.CreatedAt,
		DeprovisionedAt: entity.DeprovisionedAt,
		Metadata:        map[string]interface{}(entity.Metadata),
		Version:         entity.Version,
	}

	if entity.Subdomain != nil {
		tenant.Subdomain = *entity.Subdomain
	}

	return tenant, nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}

	// Check for GORM's duplicate key error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}

	// Check for PostgreSQL-specific unique constraint violation (code 23505)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}

	// Fallback to string matching for other drivers or wrapped errors
	errStr := err.Error()
	return strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
