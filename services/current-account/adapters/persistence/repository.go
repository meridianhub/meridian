package persistence

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository errors
var (
	ErrAccountNotFound = errors.New("account not found")
	ErrAccountExists   = errors.New("account already exists")
	ErrVersionConflict = errors.New("version conflict: account was modified by another transaction")
	ErrInvalidCursor   = errors.New("invalid pagination cursor")
)

// AccountCursor represents a pagination cursor for cursor-based pagination.
// It uses created_at + id as a composite cursor to handle ties.
type AccountCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// EncodeAccountCursor encodes a cursor to a base64 opaque page token.
func EncodeAccountCursor(c AccountCursor) string {
	data := c.CreatedAt.Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.URLEncoding.EncodeToString([]byte(data))
}

// DecodeAccountCursor decodes a base64 page token back to an AccountCursor.
// Returns ErrInvalidCursor if the token is malformed.
func DecodeAccountCursor(token string) (AccountCursor, error) {
	if token == "" {
		return AccountCursor{}, nil
	}

	data, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return AccountCursor{}, ErrInvalidCursor
	}

	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return AccountCursor{}, ErrInvalidCursor
	}

	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return AccountCursor{}, ErrInvalidCursor
	}

	id, err := uuid.Parse(parts[1])
	if err != nil {
		return AccountCursor{}, ErrInvalidCursor
	}

	return AccountCursor{CreatedAt: createdAt, ID: id}, nil
}

// ListAccountsParams holds filter and pagination parameters for listing accounts.
type ListAccountsParams struct {
	// Status filters by account lifecycle status (empty = no filter).
	Status string
	// IBAN filters by IBAN prefix match (empty = no filter).
	IBAN string
	// PartyID filters by the account owner's party ID (zero value = no filter).
	PartyID uuid.UUID
	// OrgPartyID filters by the organization party ID (zero value = no filter).
	OrgPartyID uuid.UUID
	// Limit is the maximum number of results to return.
	Limit int
	// Cursor is the pagination cursor (zero value = first page).
	Cursor AccountCursor
}

// ListAccountsResult holds the results of a ListAccounts query.
type ListAccountsResult struct {
	Accounts   []domain.CurrentAccount
	TotalCount int64
	NextCursor string
}

// Repository provides persistence operations for current accounts
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new account repository
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// DB returns the underlying database connection for transaction support.
// Use this to wrap multiple repository operations in a single transaction.
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// WithTx returns a new Repository that uses the provided transaction.
// This enables multiple repository operations within a single transaction.
//
// IMPORTANT: In multi-org mode, the repository methods (FindByIDForUpdate,
// FindByUUIDForUpdate, etc.) will automatically set the organization scope
// on the transaction. However, for optimal performance and correct behavior,
// consider setting the org scope once at the start of your transaction using
// db.WithGormTenantScope() rather than relying on per-operation scoping.
//
// Example:
//
//	err := repo.DB().Transaction(func(tx *gorm.DB) error {
//	    // Set org scope once for the entire transaction
//	    tx, err := db.WithGormTenantScope(ctx, tx)
//	    if err != nil {
//	        return err
//	    }
//	    txRepo := repo.WithTx(tx)
//	    // All operations now use the scoped transaction
//	    account, err := txRepo.FindByIDForUpdate(ctx, accountID)
//	    // ...
//	})
func (r *Repository) WithTx(tx *gorm.DB) *Repository {
	return &Repository{db: tx}
}

// withTenantScope returns a GORM DB instance scoped to the tenant from context.
// The system is always multi-tenant - tenant context is ALWAYS required.
// This sets the PostgreSQL search_path to the tenant's schema (org_<tenant_id>).
//
// This must be called within a transaction for the search_path setting to work correctly.
func (r *Repository) withTenantScope(ctx context.Context, tx *gorm.DB) (*gorm.DB, error) {
	return db.WithGormTenantScope(ctx, tx)
}

// withTenantTransaction executes the given function with tenant scoping in a transaction.
// The system is always multi-tenant - tenant context is ALWAYS required.
// This wraps the function in a transaction and sets the search_path to the tenant's schema.
func (r *Repository) withTenantTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// isInTransaction checks if the repository's db connection is already within a transaction.
// This is used to avoid creating nested transactions when the caller has already established one.
func (r *Repository) isInTransaction() bool {
	// Guard against uninitialized Statement (can happen if no query has been executed yet)
	if r.db.Statement == nil || r.db.Statement.ConnPool == nil {
		return false
	}
	// GORM sets ConnPool to a transaction object when in transaction mode.
	// In a transaction, Statement.ConnPool will be of type *sql.Tx (or GORM's tx wrapper).
	committer, ok := r.db.Statement.ConnPool.(gorm.TxCommitter)
	return ok && committer != nil
}

// withForUpdateScope executes the given function with FOR UPDATE locking support.
// If already in a transaction (via WithTx), it uses the existing transaction directly
// with tenant scope set. If not in a transaction, it creates a new one with tenant scope.
//
// This prevents the security issue where nested transactions would have search_path
// set only on the inner transaction, while the outer transaction operates without it.
func (r *Repository) withForUpdateScope(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if r.isInTransaction() {
		// Already in a transaction (via WithTx) - use it directly with tenant scope
		// The caller (e.g., lien_service.go) is responsible for the outer transaction,
		// but we still need to set the tenant scope for this operation.
		// Note: withTenantScope returns already-wrapped errors, so don't re-wrap.
		tx, err := r.withTenantScope(ctx, r.db.WithContext(ctx))
		if err != nil {
			return err
		}
		return fn(tx)
	}

	// Not in a transaction - use the shared helper that handles transaction + tenant scope
	return db.WithGormTenantTransaction(ctx, r.db, fn)
}

// Save creates or updates an account with optimistic locking.
// The context is used to extract audit information (user ID) for the created_by/updated_by fields.
// In multi-org mode, the context must contain the organization ID for schema routing.
//
// For updates, the version in the domain model must match the version in the database.
// If another transaction has modified the record (incremented the version), this save
// will fail with ErrVersionConflict. The caller should reload the entity and retry.
//
// Alternative: Use FindByIDForUpdate() with SELECT FOR UPDATE for pessimistic locking
// within a transaction when you need guaranteed exclusive access.
func (r *Repository) Save(ctx context.Context, account domain.CurrentAccount) error {
	entity, err := toEntity(ctx, account)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Set organization scope if in multi-org mode
		// Note: withTenantScope returns already-wrapped errors from db.WithGormTenantScope
		tx, err := r.withTenantScope(ctx, tx)
		if err != nil {
			return err
		}

		// Check if exists by account_identification (IBAN)
		var existing CurrentAccountEntity
		result := tx.Where("account_identification = ?", entity.AccountIdentification).First(&existing)

		if result.Error == nil {
			// Update existing with optimistic locking
			entity.ID = existing.ID
			entity.CreatedAt = existing.CreatedAt
			entity.CreatedBy = existing.CreatedBy

			// Optimistic locking: domain already incremented version during mutation.
			// Check against original version (entity.Version - 1), then set to new version.
			// Note: Balance fields not persisted - balance computation delegated to Position Keeping service.
			originalVersion := entity.Version - 1
			updateResult := tx.Model(&CurrentAccountEntity{}).
				Where("account_identification = ? AND version = ?", entity.AccountIdentification, originalVersion).
				Updates(map[string]interface{}{
					"status":         entity.Status,
					"freeze_reason":  entity.FreezeReason,
					"status_history": entity.StatusHistory,
					"version":        entity.Version,
					"updated_at":     entity.UpdatedAt,
					"updated_by":     entity.UpdatedBy,
				})

			if updateResult.Error != nil {
				return updateResult.Error
			}

			// If no rows were affected, the version didn't match (concurrent modification)
			if updateResult.RowsAffected == 0 {
				return ErrVersionConflict
			}

			return nil
		}

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Create new - version starts at 1 (set by toEntity)
			if err := tx.Create(&entity).Error; err != nil {
				// Handle race condition: another transaction created the same account
				if isDuplicateKeyError(err) {
					return ErrAccountExists
				}
				return err
			}
			return nil
		}

		return result.Error
	})
}

// FindByID retrieves an account by its internal account ID (e.g., "ACC-xxx").
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByID(ctx context.Context, accountID string) (domain.CurrentAccount, error) {
	var account domain.CurrentAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Where("account_id = ? AND deleted_at IS NULL", accountID).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return domain.CurrentAccount{}, err
	}
	return account, nil
}

// FindByIDForUpdate retrieves an account by its internal account ID with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
// In multi-org mode, the context must contain the organization ID for schema routing.
//
// IMPORTANT: This method expects to be called within an existing transaction that already
// has the organization scope set. When using WithTx(), the caller is responsible for setting
// the org scope on the outer transaction. This method will set the org scope if not already
// in a transaction, but when called via WithTx(), it uses the existing transaction directly.
func (r *Repository) FindByIDForUpdate(ctx context.Context, accountID string) (domain.CurrentAccount, error) {
	var account domain.CurrentAccount

	// Perform the FOR UPDATE query, wrapping in org-scoped transaction if needed
	err := r.withForUpdateScope(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_id = ? AND deleted_at IS NULL", accountID).
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return domain.CurrentAccount{}, err
	}
	return account, nil
}

// FindByIBAN retrieves an account by its IBAN (stored in account_identification column).
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByIBAN(ctx context.Context, iban string) (domain.CurrentAccount, error) {
	var account domain.CurrentAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Where("account_identification = ? AND deleted_at IS NULL", iban).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return domain.CurrentAccount{}, err
	}
	return account, nil
}

// FindByUUID retrieves an account by its internal UUID.
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByUUID(ctx context.Context, id uuid.UUID) (domain.CurrentAccount, error) {
	var account domain.CurrentAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Where("id = ? AND deleted_at IS NULL", id).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return domain.CurrentAccount{}, err
	}
	return account, nil
}

// FindByUUIDForUpdate retrieves an account by its internal UUID with a pessimistic lock.
// Use this within a transaction when you need to prevent concurrent modifications.
// In multi-org mode, the context must contain the organization ID for schema routing.
//
// IMPORTANT: This method expects to be called within an existing transaction that already
// has the organization scope set. When using WithTx(), the caller is responsible for setting
// the org scope on the outer transaction. This method will set the org scope if not already
// in a transaction, but when called via WithTx(), it uses the existing transaction directly.
func (r *Repository) FindByUUIDForUpdate(ctx context.Context, id uuid.UUID) (domain.CurrentAccount, error) {
	var account domain.CurrentAccount

	// Perform the FOR UPDATE query, wrapping in org-scoped transaction if needed
	err := r.withForUpdateScope(ctx, func(tx *gorm.DB) error {
		var entity CurrentAccountEntity
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", id).
			First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}

		if result.Error != nil {
			return result.Error
		}

		var err error
		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return domain.CurrentAccount{}, err
	}
	return account, nil
}

// FindByPartyID retrieves all accounts for a party.
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByPartyID(ctx context.Context, partyID string) ([]domain.CurrentAccount, error) {
	var accounts []domain.CurrentAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []CurrentAccountEntity
		result := tx.Where("party_id = ? AND deleted_at IS NULL", partyID).Find(&entities)

		if result.Error != nil {
			return result.Error
		}

		accounts = make([]domain.CurrentAccount, 0, len(entities))
		for _, entity := range entities {
			account, err := toDomain(&entity)
			if err != nil {
				return err
			}
			accounts = append(accounts, account)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// FindByScopedParty retrieves an account by party ID, org party ID, and instrument code.
// This supports org-scoped account lookups where an individual (partyID) holds
// an account within an organization (orgPartyID) for a specific instrument (e.g. GBP, kWh).
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) FindByScopedParty(ctx context.Context, partyID string, orgPartyID uuid.UUID, instrumentCode string) (domain.CurrentAccount, error) {
	var account domain.CurrentAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		partyUUID, err := uuid.Parse(partyID)
		if err != nil {
			return fmt.Errorf("invalid party ID %q: %w", partyID, err)
		}

		var entity CurrentAccountEntity
		result := tx.Where("party_id = ? AND org_party_id = ? AND instrument_code = ? AND deleted_at IS NULL",
			partyUUID, orgPartyID, instrumentCode).First(&entity)

		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ErrAccountNotFound
		}
		if result.Error != nil {
			return result.Error
		}

		account, err = toDomain(&entity)
		return err
	})
	if err != nil {
		return domain.CurrentAccount{}, err
	}
	return account, nil
}

// ListByOrganization retrieves all accounts scoped to an organization.
// This returns all accounts where org_party_id matches, supporting NFR-2
// (list all syndicate participant accounts for an organization).
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) ListByOrganization(ctx context.Context, orgPartyID uuid.UUID) ([]domain.CurrentAccount, error) {
	var accounts []domain.CurrentAccount
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		var entities []CurrentAccountEntity
		result := tx.Where("org_party_id = ? AND deleted_at IS NULL", orgPartyID).Find(&entities)

		if result.Error != nil {
			return result.Error
		}

		accounts = make([]domain.CurrentAccount, 0, len(entities))
		for _, entity := range entities {
			account, err := toDomain(&entity)
			if err != nil {
				return err
			}
			accounts = append(accounts, account)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// ListAccounts retrieves a paginated list of accounts with optional filtering.
// Results are ordered by created_at DESC, id DESC (newest first) for stable pagination.
// The context must contain the tenant ID for schema routing.
func (r *Repository) ListAccounts(ctx context.Context, params ListAccountsParams) (*ListAccountsResult, error) {
	var result *ListAccountsResult
	err := r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		limit := params.Limit
		if limit <= 0 {
			limit = 25
		}

		// Base query: exclude soft-deleted accounts
		baseQuery := tx.Model(&CurrentAccountEntity{}).Where("deleted_at IS NULL")

		// Apply filters
		if params.Status != "" {
			baseQuery = baseQuery.Where("status = ?", params.Status)
		}
		if params.IBAN != "" {
			baseQuery = baseQuery.Where("account_identification LIKE ?", params.IBAN+"%")
		}
		if params.PartyID != uuid.Nil {
			baseQuery = baseQuery.Where("party_id = ?", params.PartyID)
		}
		if params.OrgPartyID != uuid.Nil {
			baseQuery = baseQuery.Where("org_party_id = ?", params.OrgPartyID)
		}

		// Get total count matching filters
		var totalCount int64
		if err := baseQuery.Count(&totalCount).Error; err != nil {
			return err
		}

		if totalCount == 0 {
			result = &ListAccountsResult{
				Accounts:   []domain.CurrentAccount{},
				TotalCount: 0,
				NextCursor: "",
			}
			return nil
		}

		// Apply cursor for pagination
		pageQuery := baseQuery
		if !params.Cursor.CreatedAt.IsZero() {
			// Composite cursor: items before cursor position in DESC order
			pageQuery = pageQuery.Where(
				"(created_at < ?) OR (created_at = ? AND id < ?)",
				params.Cursor.CreatedAt, params.Cursor.CreatedAt, params.Cursor.ID,
			)
		}

		var entities []CurrentAccountEntity
		if err := pageQuery.
			Order("created_at DESC, id DESC").
			Limit(limit + 1). // fetch one extra to detect next page
			Find(&entities).Error; err != nil {
			return err
		}

		hasMore := len(entities) > limit
		if hasMore {
			entities = entities[:limit]
		}

		accounts := make([]domain.CurrentAccount, 0, len(entities))
		for i := range entities {
			account, err := toDomain(&entities[i])
			if err != nil {
				return err
			}
			accounts = append(accounts, account)
		}

		var nextCursor string
		if hasMore && len(entities) > 0 {
			last := entities[len(entities)-1]
			nextCursor = EncodeAccountCursor(AccountCursor{
				CreatedAt: last.CreatedAt,
				ID:        last.ID,
			})
		}

		result = &ListAccountsResult{
			Accounts:   accounts,
			TotalCount: totalCount,
			NextCursor: nextCursor,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Delete soft deletes an account by its internal account ID.
// In multi-org mode, the context must contain the organization ID for schema routing.
func (r *Repository) Delete(ctx context.Context, accountID string) error {
	return r.withTenantTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&CurrentAccountEntity{}).
			Where("account_id = ?", accountID).
			Update("deleted_at", time.Now()).Error
	})
}

// Ping checks database connectivity without triggering record-not-found logging.
// This is used by health checks to verify the database is reachable.
func (r *Repository) Ping() error {
	var result int
	return r.db.Raw("SELECT 1").Scan(&result).Error
}
