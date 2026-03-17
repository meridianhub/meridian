package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/tenant/domain"
	dbpkg "github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

// Repository errors.
var (
	ErrTenantNotFound  = errors.New("tenant not found")
	ErrTenantExists    = errors.New("tenant already exists")
	ErrVersionConflict = errors.New("version conflict: tenant was modified by another transaction")
	ErrSubdomainTaken  = errors.New("subdomain already taken by another tenant")
	ErrSlugTaken       = errors.New("slug already taken by another tenant")
)

// Compile-time check that Repository implements domain.TenantRepository.
var _ domain.TenantRepository = (*Repository)(nil)

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

// conn returns a GORM session with tenant guard bypass applied.
// The tenant service operates at the platform level (managing tenants),
// so all its queries bypass tenant-scoped search_path restrictions.
func (r *Repository) conn(ctx context.Context) *gorm.DB {
	return r.db.WithContext(dbpkg.WithTenantGuardBypass(ctx))
}

// WithTx returns a new Repository that uses the provided transaction.
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// Create registers a new tenant (BIAN: Initiate).
func (r *Repository) Create(ctx context.Context, tenant *domain.Tenant) error {
	entity := toEntity(tenant)

	if err := r.conn(ctx).Create(&entity).Error; err != nil {
		if isDuplicateKeyError(err) {
			if strings.Contains(err.Error(), "slug") {
				return ErrSlugTaken
			}
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
func (r *Repository) GetByID(ctx context.Context, id tenant.TenantID) (*domain.Tenant, error) {
	var entity TenantEntity
	result := r.conn(ctx).Where("id = ?", id.String()).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrTenantNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// GetBySlug retrieves a tenant by its URL-friendly slug identifier.
// Uses the idx_tenant_slug index for fast lookups.
// Returns ErrTenantNotFound for empty slugs (fail-fast).
// Slug lookup is case-insensitive: input is normalized to lowercase before querying.
func (r *Repository) GetBySlug(ctx context.Context, slug string) (*domain.Tenant, error) {
	if slug == "" {
		return nil, ErrTenantNotFound
	}

	// Normalize to lowercase since slugs are stored lowercase
	slug = strings.ToLower(slug)

	var entity TenantEntity
	result := r.conn(ctx).Where("slug = ?", slug).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrTenantNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// IsSlugAvailable checks if a slug is available for registration.
// Returns true if the slug is not in use, false if it's taken.
// Returns an error only if the database query fails.
// Returns false for empty slugs (invalid input).
// Slug lookup is case-insensitive: input is normalized to lowercase before querying.
func (r *Repository) IsSlugAvailable(ctx context.Context, slug string) (bool, error) {
	if slug == "" {
		return false, nil
	}

	// Normalize to lowercase since slugs are stored lowercase
	slug = strings.ToLower(slug)

	var count int64
	result := r.conn(ctx).
		Model(&TenantEntity{}).
		Where("slug = ?", slug).
		Count(&count)

	if result.Error != nil {
		return false, result.Error
	}

	return count == 0, nil
}

// IsActive checks if a tenant exists and is active.
// This is optimized for validation middleware - returns only what's needed.
func (r *Repository) IsActive(ctx context.Context, id tenant.TenantID) (bool, error) {
	var status string
	result := r.conn(ctx).
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
func (r *Repository) UpdateStatus(ctx context.Context, id tenant.TenantID, status domain.Status, currentVersion int) (*domain.Tenant, error) {
	updates := map[string]interface{}{
		"status":  status,
		"version": currentVersion + 1,
	}

	// Set deprovisioned_at timestamp when deprovisioning
	if status == domain.StatusDeprovisioned {
		now := time.Now()
		updates["deprovisioned_at"] = &now
	}

	// Clear error message when status is not a failure state
	if status != domain.StatusProvisioningFailed {
		updates["error_message"] = nil
	}

	result := r.conn(ctx).
		Model(&TenantEntity{}).
		Where("id = ? AND version = ?", id.String(), currentVersion).
		Updates(updates)

	if result.Error != nil {
		return nil, result.Error
	}

	if result.RowsAffected == 0 {
		// Check if tenant exists
		var count int64
		if err := r.conn(ctx).
			Model(&TenantEntity{}).
			Where("id = ?", id.String()).
			Count(&count).Error; err != nil {
			return nil, err
		}

		if count == 0 {
			return nil, ErrTenantNotFound
		}
		return nil, ErrVersionConflict
	}

	// Fetch and return the updated tenant
	return r.GetByID(ctx, id)
}

// UpdateStatusWithError changes the tenant status and sets an error message.
// Used for recording provisioning failures.
func (r *Repository) UpdateStatusWithError(ctx context.Context, id tenant.TenantID, status domain.Status, errorMessage string, currentVersion int) (*domain.Tenant, error) {
	updates := map[string]interface{}{
		"status":        status,
		"error_message": errorMessage,
		"version":       currentVersion + 1,
	}

	result := r.conn(ctx).
		Model(&TenantEntity{}).
		Where("id = ? AND version = ?", id.String(), currentVersion).
		Updates(updates)

	if result.Error != nil {
		return nil, result.Error
	}

	if result.RowsAffected == 0 {
		// Check if tenant exists
		var count int64
		if err := r.conn(ctx).
			Model(&TenantEntity{}).
			Where("id = ?", id.String()).
			Count(&count).Error; err != nil {
			return nil, err
		}

		if count == 0 {
			return nil, ErrTenantNotFound
		}
		return nil, ErrVersionConflict
	}

	// Fetch and return the updated tenant
	return r.GetByID(ctx, id)
}

// ListByStatus returns up to limit tenants with the given status.
// Used by the provisioning worker to fetch pending tenants.
// Returns empty slice if no tenants found (not an error).
func (r *Repository) ListByStatus(ctx context.Context, status domain.Status, limit int) ([]*domain.Tenant, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 1000 {
		limit = 1000
	}

	var entities []TenantEntity
	result := r.conn(ctx).
		Where("status = ?", status).
		Order("created_at ASC").
		Limit(limit).
		Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	// Convert to domain models
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

// ListByStatusOlderThan returns tenants with the given status last updated before the specified cutoff time.
// Used by the alert manager to identify tenants stuck in a failed state for extended periods.
// Returns empty slice if no tenants found (not an error).
// The cutoff parameter filters tenants WHERE updated_at < cutoff.
// Using updated_at is more accurate than created_at because it reflects when the status last changed.
// Limited to 100 results to prevent large result sets in degraded states.
func (r *Repository) ListByStatusOlderThan(ctx context.Context, status domain.Status, cutoff time.Time) ([]*domain.Tenant, error) {
	var entities []TenantEntity
	result := r.conn(ctx).
		Where("status = ? AND updated_at < ?", status, cutoff).
		Order("updated_at ASC").
		Limit(100). // Prevent large result sets in degraded states
		Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	// Convert to domain models
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

// List returns tenants with optional status filter (BIAN: Control).
func (r *Repository) List(ctx context.Context, statusFilter *domain.Status, pageSize int, pageToken string) ([]*domain.Tenant, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	query := r.conn(ctx).Model(&TenantEntity{})

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
	if err := r.conn(ctx).Find(&entities).Error; err != nil {
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

// FindProvisioningStatusByTenantID retrieves all per-service provisioning status records for a tenant.
// Returns an empty slice if no provisioning status records exist (not an error).
func (r *Repository) FindProvisioningStatusByTenantID(ctx context.Context, tenantID string) ([]domain.ProvisioningStatus, error) {
	var entities []ProvisioningStatusEntity
	result := r.conn(ctx).
		Where("tenant_id = ?", tenantID).
		Order("service_name").
		Find(&entities)

	if result.Error != nil {
		return nil, result.Error
	}

	// Empty result is not an error - tenant may not have any provisioning status records yet
	if len(entities) == 0 {
		return []domain.ProvisioningStatus{}, nil
	}

	// Convert entities to domain models
	statuses := make([]domain.ProvisioningStatus, 0, len(entities))
	for i := range entities {
		status := provisioningStatusToDomain(&entities[i])
		statuses = append(statuses, status)
	}

	return statuses, nil
}

// Ping checks database connectivity.
func (r *Repository) Ping(ctx context.Context) error {
	var result int
	return r.conn(ctx).Raw("SELECT 1").Scan(&result).Error
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

	if tenant.Slug != "" {
		entity.Slug = &tenant.Slug
	}

	if tenant.PartyID != "" {
		entity.PartyID = &tenant.PartyID
	}

	if tenant.ErrorMessage != "" {
		entity.ErrorMessage = &tenant.ErrorMessage
	}

	return entity
}

// toDomain converts database entity to domain model.
func toDomain(entity *TenantEntity) (*domain.Tenant, error) {
	tenantID, err := tenant.NewTenantID(entity.ID)
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

	if entity.Slug != nil {
		tenant.Slug = *entity.Slug
	}

	if entity.PartyID != nil {
		tenant.PartyID = *entity.PartyID
	}

	if entity.ErrorMessage != nil {
		tenant.ErrorMessage = *entity.ErrorMessage
	}

	return tenant, nil
}

// provisioningStatusToDomain converts database entity to domain model.
func provisioningStatusToDomain(entity *ProvisioningStatusEntity) domain.ProvisioningStatus {
	status := domain.ProvisioningStatus{
		ServiceName: entity.ServiceName,
		Status:      domain.ServiceProvisioningStatus(entity.Status),
		StartedAt:   entity.StartedAt,
		CompletedAt: entity.CompletedAt,
	}

	// Handle optional fields
	if entity.MigrationVersion != nil {
		status.MigrationVersion = *entity.MigrationVersion
	}

	if entity.ErrorMessage != nil {
		status.ErrorMessage = entity.ErrorMessage
	}

	return status
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
