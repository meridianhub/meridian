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
	"github.com/meridianhub/meridian/shared/platform/audit"
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
					"status":          entity.Status,
					"freeze_reason":   entity.FreezeReason,
					"status_history":  entity.StatusHistory,
					"overdraft_limit": entity.OverdraftLimit,
					"overdraft_rate":  entity.OverdraftRate,
					"version":         entity.Version,
					"updated_at":      entity.UpdatedAt,
					"updated_by":      entity.UpdatedBy,
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

// toEntity converts domain model to database entity
// Note: The entity schema matches migrations/current_account/*.sql
// OverdraftEnabled is derived from OverdraftLimit > 0
func toEntity(ctx context.Context, account domain.CurrentAccount) (*CurrentAccountEntity, error) {
	// Parse PartyID as UUID - domain model uses string for flexibility
	partyUUID, err := uuid.Parse(account.PartyID())
	if err != nil {
		return nil, fmt.Errorf("invalid party ID %q: %w", account.PartyID(), err)
	}

	// Extract audit user from context (falls back to "system" if not available)
	auditUser := audit.GetUserFromContext(ctx)

	// Convert domain StatusHistory to persistence StatusHistoryJSON
	domainHistory := account.StatusHistory()
	statusHistory := make(StatusHistoryJSON, len(domainHistory))
	for i, change := range domainHistory {
		statusHistory[i] = StatusHistoryEntry{
			FromStatus: string(change.From),
			ToStatus:   string(change.To),
			Reason:     change.Reason,
			Timestamp:  change.Timestamp,
			ChangedBy:  auditUser,
		}
	}

	// Handle freeze reason - nil if empty
	var freezeReason *string
	if account.FreezeReason() != "" {
		reason := account.FreezeReason()
		freezeReason = &reason
	}

	// Map product type fields (nil if empty for backwards compatibility)
	var productTypeCode *string
	var productTypeVersion *int
	if account.ProductTypeCode() != "" {
		code := account.ProductTypeCode()
		productTypeCode = &code
		version := account.ProductTypeVersion()
		productTypeVersion = &version
	}

	// ToMinorUnitsUnchecked is safe here: domain layer validates amounts before persistence,
	// so overflow (>92 quadrillion cents) cannot occur for valid accounts
	// Note: Balance fields are not persisted to DB (gorm:"-") but kept on entity for
	// in-memory round-trip. Position Keeping is now the source of truth for balances.
	return &CurrentAccountEntity{
		ID:                    account.ID(),
		AccountID:             account.AccountID(),             // Business account identifier
		AccountIdentification: account.AccountIdentification(), // IBAN stored in account_identification
		AccountType:           "current",                       // Default for current accounts
		InstrumentCode:        account.Balance().CurrencyCode(),
		Dimension:             account.Balance().Quantity().Instrument.Dimension,
		Status:                string(account.Status()),
		PartyID:               partyUUID,
		OrgPartyID:            account.OrgPartyID(),
		OverdraftLimit:        account.OverdraftLimit().ToMinorUnitsUnchecked(),
		OverdraftRate:         account.OverdraftRate(),
		ProductTypeCode:       productTypeCode,
		ProductTypeVersion:    productTypeVersion,
		Balance:               account.Balance().ToMinorUnitsUnchecked(),          // gorm:"-" - not persisted
		AvailableBalance:      account.AvailableBalance().ToMinorUnitsUnchecked(), // gorm:"-" - not persisted
		FreezeReason:          freezeReason,
		StatusHistory:         statusHistory,
		Version:               account.Version(),
		CreatedAt:             account.CreatedAt(),
		UpdatedAt:             account.UpdatedAt(),
		CreatedBy:             auditUser,
		UpdatedBy:             auditUser,
	}, nil
}

// toDomain converts database entity to domain model using the builder pattern.
// Note: OverdraftEnabled is derived from OverdraftLimit > 0
// Note: Balance fields are not persisted - balance computation delegated to Position Keeping service.
// The service layer is responsible for populating balance from Position Keeping after retrieval.
func toDomain(entity *CurrentAccountEntity) (domain.CurrentAccount, error) {
	// Balance fields are no longer persisted to the database.
	// Use entity's in-memory balance fields if populated (e.g., from recent save),
	// otherwise initialize with zero values.
	// The service layer should populate from Position Keeping for authoritative balance.
	balance, err := domain.NewMoney(entity.InstrumentCode, entity.Balance)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create balance: %w", err)
	}
	availableBalance, err := domain.NewMoney(entity.InstrumentCode, entity.AvailableBalance)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create available balance: %w", err)
	}

	overdraftLimit, err := domain.NewMoney(entity.InstrumentCode, entity.OverdraftLimit)
	if err != nil {
		return domain.CurrentAccount{}, fmt.Errorf("failed to create overdraft limit from database: %w", err)
	}

	// Derive overdraft enabled from limit > 0
	overdraftEnabled := entity.OverdraftLimit > 0

	// Balance is now computed by Position Keeping service, so use current time as placeholder.
	// The service layer will update this when fetching balance from Position Keeping.
	balanceUpdatedAt := entity.UpdatedAt

	// Convert persistence StatusHistoryJSON to domain StatusHistory
	var statusHistory []domain.StatusChange
	if len(entity.StatusHistory) > 0 {
		statusHistory = make([]domain.StatusChange, len(entity.StatusHistory))
		for i, entry := range entity.StatusHistory {
			statusHistory[i] = domain.StatusChange{
				From:      domain.AccountStatus(entry.FromStatus),
				To:        domain.AccountStatus(entry.ToStatus),
				Reason:    entry.Reason,
				Timestamp: entry.Timestamp,
				ChangedBy: entry.ChangedBy,
			}
		}
	}

	// Handle freeze reason - empty string if nil
	freezeReason := ""
	if entity.FreezeReason != nil {
		freezeReason = *entity.FreezeReason
	}

	// Map product type fields (empty string / 0 if nil for domain model)
	productTypeCode := ""
	productTypeVersion := 0
	if entity.ProductTypeCode != nil {
		productTypeCode = *entity.ProductTypeCode
	}
	if entity.ProductTypeVersion != nil {
		productTypeVersion = *entity.ProductTypeVersion
	}

	// Use builder pattern to construct immutable domain model
	// Note: Balance comes from entity's in-memory fields (gorm:"-") if populated,
	// otherwise zero. Service layer should fetch authoritative balance from Position Keeping.
	return domain.NewCurrentAccountBuilder().
		WithID(entity.ID).
		WithAccountID(entity.AccountID).
		WithAccountIdentification(entity.AccountIdentification).
		WithPartyID(entity.PartyID.String()).
		WithOrgPartyID(entity.OrgPartyID).
		WithBalance(balance).
		WithAvailableBalance(availableBalance).
		WithStatus(domain.AccountStatus(entity.Status)).
		WithFreezeReason(freezeReason).
		WithStatusHistory(statusHistory).
		WithOverdraftLimit(overdraftLimit).
		WithOverdraftEnabled(overdraftEnabled).
		WithOverdraftRate(entity.OverdraftRate).
		WithBalanceUpdatedAt(balanceUpdatedAt).
		WithVersion(entity.Version).
		WithCreatedAt(entity.CreatedAt).
		WithUpdatedAt(entity.UpdatedAt).
		WithProductTypeCode(productTypeCode).
		WithProductTypeVersion(productTypeVersion).
		Build(), nil
}

// isDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
// This handles the race condition where two concurrent creates attempt to insert
// the same account_identification (IBAN).
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// PostgreSQL unique violation error code is 23505
	// GORM wraps this, so we check the error message
	errStr := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
