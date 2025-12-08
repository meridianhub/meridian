package persistence

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/organization/domain"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"gorm.io/gorm"
)

// Repository errors.
var (
	ErrOrganizationNotFound = errors.New("organization not found")
	ErrOrganizationExists   = errors.New("organization already exists")
	ErrVersionConflict      = errors.New("version conflict: organization was modified by another transaction")
	ErrSubdomainTaken       = errors.New("subdomain already taken by another organization")
)

// Repository provides persistence operations for organizations.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new organization repository.
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

// Create registers a new organization (BIAN: Initiate).
func (r *Repository) Create(ctx context.Context, org *domain.Organization) error {
	entity := toEntity(org)

	if err := r.db.WithContext(ctx).Create(&entity).Error; err != nil {
		if isDuplicateKeyError(err) {
			if strings.Contains(err.Error(), "subdomain") {
				return ErrSubdomainTaken
			}
			return ErrOrganizationExists
		}
		return err
	}

	// Update domain model with created timestamp
	org.CreatedAt = entity.CreatedAt
	org.Version = entity.Version

	return nil
}

// GetByID retrieves an organization by ID (BIAN: Retrieve).
func (r *Repository) GetByID(ctx context.Context, id organization.OrganizationID) (*domain.Organization, error) {
	var entity OrganizationEntity
	result := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrOrganizationNotFound
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return toDomain(&entity)
}

// IsActive checks if an organization exists and is active.
// This is optimized for validation middleware - returns only what's needed.
func (r *Repository) IsActive(ctx context.Context, id organization.OrganizationID) (bool, error) {
	var status string
	result := r.db.WithContext(ctx).
		Model(&OrganizationEntity{}).
		Select("status").
		Where("id = ?", id.String()).
		Take(&status)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false, ErrOrganizationNotFound
	}

	if result.Error != nil {
		return false, result.Error
	}

	return status == string(domain.StatusActive), nil
}

// UpdateStatus changes the organization status (BIAN: Update).
func (r *Repository) UpdateStatus(ctx context.Context, id organization.OrganizationID, status domain.Status, currentVersion int) (*domain.Organization, error) {
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
		Model(&OrganizationEntity{}).
		Where("id = ? AND version = ?", id.String(), currentVersion).
		Updates(updates)

	if result.Error != nil {
		return nil, result.Error
	}

	if result.RowsAffected == 0 {
		// Check if organization exists
		var count int64
		r.db.WithContext(ctx).
			Model(&OrganizationEntity{}).
			Where("id = ?", id.String()).
			Count(&count)

		if count == 0 {
			return nil, ErrOrganizationNotFound
		}
		return nil, ErrVersionConflict
	}

	// Fetch and return the updated organization
	return r.GetByID(ctx, id)
}

// List returns organizations with optional status filter (BIAN: Control).
func (r *Repository) List(ctx context.Context, statusFilter *domain.Status, pageSize int, pageToken string) ([]*domain.Organization, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	query := r.db.WithContext(ctx).Model(&OrganizationEntity{})

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

	var entities []OrganizationEntity
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
	orgs := make([]*domain.Organization, 0, len(entities))
	for i := range entities {
		org, err := toDomain(&entities[i])
		if err != nil {
			return nil, "", err
		}
		orgs = append(orgs, org)
	}

	return orgs, nextPageToken, nil
}

// GetAll returns all organizations (for cache initialization).
func (r *Repository) GetAll(ctx context.Context) ([]*domain.Organization, error) {
	var entities []OrganizationEntity
	if err := r.db.WithContext(ctx).Find(&entities).Error; err != nil {
		return nil, err
	}

	orgs := make([]*domain.Organization, 0, len(entities))
	for i := range entities {
		org, err := toDomain(&entities[i])
		if err != nil {
			return nil, err
		}
		orgs = append(orgs, org)
	}

	return orgs, nil
}

// Ping checks database connectivity.
func (r *Repository) Ping(ctx context.Context) error {
	var result int
	return r.db.WithContext(ctx).Raw("SELECT 1").Scan(&result).Error
}

// toEntity converts domain model to database entity.
func toEntity(org *domain.Organization) *OrganizationEntity {
	entity := &OrganizationEntity{
		ID:              org.ID.String(),
		DisplayName:     org.DisplayName,
		SettlementAsset: org.SettlementAsset,
		Status:          string(org.Status),
		CreatedAt:       org.CreatedAt,
		DeprovisionedAt: org.DeprovisionedAt,
		Metadata:        JSONMap(org.Metadata),
		Version:         org.Version,
	}

	if org.Subdomain != "" {
		entity.Subdomain = &org.Subdomain
	}

	return entity
}

// toDomain converts database entity to domain model.
func toDomain(entity *OrganizationEntity) (*domain.Organization, error) {
	orgID, err := organization.NewOrganizationID(entity.ID)
	if err != nil {
		return nil, err
	}

	org := &domain.Organization{
		ID:              orgID,
		DisplayName:     entity.DisplayName,
		SettlementAsset: entity.SettlementAsset,
		Status:          domain.Status(entity.Status),
		CreatedAt:       entity.CreatedAt,
		DeprovisionedAt: entity.DeprovisionedAt,
		Metadata:        map[string]interface{}(entity.Metadata),
		Version:         entity.Version,
	}

	if entity.Subdomain != nil {
		org.Subdomain = *entity.Subdomain
	}

	return org, nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
