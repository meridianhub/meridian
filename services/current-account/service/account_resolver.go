// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	internalbankaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
)

// AccountResolverErrors defines sentinel errors for the AccountResolver.
var (
	// ErrNoClearingAccountFound is returned when no active clearing account is found for the given criteria.
	ErrNoClearingAccountFound = errors.New("no active clearing account found")

	// ErrAccountResolverClientNil is returned when attempting to create an AccountResolver with a nil client.
	ErrAccountResolverClientNil = errors.New("internal bank account client cannot be nil")

	// ErrAccountResolverLoggerNil is returned when attempting to create an AccountResolver with a nil logger.
	ErrAccountResolverLoggerNil = errors.New("logger cannot be nil")
)

// ClearingAccountType defines the type of clearing account being resolved.
type ClearingAccountType string

const (
	// ClearingAccountTypeDeposit identifies the clearing account for deposit operations.
	ClearingAccountTypeDeposit ClearingAccountType = "DEPOSIT"

	// ClearingAccountTypeWithdrawal identifies the clearing account for withdrawal operations.
	ClearingAccountTypeWithdrawal ClearingAccountType = "WITHDRAWAL"
)

// cacheEntry holds a cached account ID with its expiration time.
type cacheEntry struct {
	accountID string
	expiresAt time.Time
}

// AccountResolver resolves clearing account IDs dynamically from the Internal Bank Account service.
// It provides caching to reduce external service calls and supports both deposit and withdrawal
// clearing account lookups.
//
// Thread-safe: All methods can be called concurrently from multiple goroutines.
type AccountResolver struct {
	client InternalBankAccountClient
	logger *slog.Logger

	// Cache configuration
	cacheTTL time.Duration

	// Thread-safe cache: key is "TYPE:INSTRUMENT" (e.g., "DEPOSIT:GBP")
	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// AccountResolverConfig holds configuration for creating an AccountResolver.
type AccountResolverConfig struct {
	// Client is the Internal Bank Account gRPC client.
	Client InternalBankAccountClient

	// Logger is used for logging resolver operations.
	Logger *slog.Logger

	// CacheTTL is how long resolved account IDs are cached.
	// Defaults to 5 minutes if not specified.
	CacheTTL time.Duration
}

// DefaultCacheTTL is the default cache TTL for resolved account IDs.
const DefaultCacheTTL = 5 * time.Minute

// NewAccountResolver creates a new AccountResolver with the given configuration.
// Returns an error if required configuration is missing.
func NewAccountResolver(cfg AccountResolverConfig) (*AccountResolver, error) {
	if cfg.Client == nil {
		return nil, ErrAccountResolverClientNil
	}
	if cfg.Logger == nil {
		return nil, ErrAccountResolverLoggerNil
	}

	ttl := cfg.CacheTTL
	if ttl == 0 {
		ttl = DefaultCacheTTL
	}

	return &AccountResolver{
		client:   cfg.Client,
		logger:   cfg.Logger,
		cacheTTL: ttl,
		cache:    make(map[string]cacheEntry),
	}, nil
}

// GetDepositClearingAccount resolves the clearing account ID for deposit operations
// for the given instrument (currency/asset code like "GBP", "USD", "KWH").
//
// It first checks the cache, and if not found or expired, queries the Internal Bank Account
// service for an active CLEARING account matching the instrument.
func (r *AccountResolver) GetDepositClearingAccount(ctx context.Context, instrumentCode string) (string, error) {
	return r.resolveClearingAccount(ctx, ClearingAccountTypeDeposit, instrumentCode)
}

// GetWithdrawalClearingAccount resolves the clearing account ID for withdrawal operations
// for the given instrument (currency/asset code like "GBP", "USD", "KWH").
//
// It first checks the cache, and if not found or expired, queries the Internal Bank Account
// service for an active CLEARING account matching the instrument.
func (r *AccountResolver) GetWithdrawalClearingAccount(ctx context.Context, instrumentCode string) (string, error) {
	return r.resolveClearingAccount(ctx, ClearingAccountTypeWithdrawal, instrumentCode)
}

// resolveClearingAccount is the internal implementation for resolving clearing accounts.
func (r *AccountResolver) resolveClearingAccount(ctx context.Context, clearingType ClearingAccountType, instrumentCode string) (string, error) {
	cacheKey := r.cacheKey(clearingType, instrumentCode)

	// Check cache first (read lock)
	r.mu.RLock()
	if entry, ok := r.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		r.mu.RUnlock()
		r.logger.Debug("cache hit for clearing account",
			"clearing_type", clearingType,
			"instrument_code", instrumentCode,
			"account_id", entry.accountID)
		caobservability.RecordClearingAccountCacheHit()
		return entry.accountID, nil
	}
	r.mu.RUnlock()

	caobservability.RecordClearingAccountCacheMiss()

	// Query the Internal Bank Account service
	start := time.Now()
	accountID, err := r.queryInternalBankAccount(ctx, clearingType, instrumentCode)
	if err != nil {
		caobservability.RecordClearingAccountLookupError(string(clearingType))
		return "", err
	}
	caobservability.RecordClearingAccountLookupDuration(time.Since(start))

	// Cache the result (write lock)
	r.mu.Lock()
	r.cache[cacheKey] = cacheEntry{
		accountID: accountID,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	r.mu.Unlock()

	r.logger.Info("resolved clearing account",
		"clearing_type", clearingType,
		"instrument_code", instrumentCode,
		"account_id", accountID,
		"duration_ms", time.Since(start).Milliseconds())

	return accountID, nil
}

// queryInternalBankAccount queries the Internal Bank Account service for a clearing account.
func (r *AccountResolver) queryInternalBankAccount(ctx context.Context, clearingType ClearingAccountType, instrumentCode string) (string, error) {
	resp, err := r.client.ListInternalBankAccounts(ctx, &internalbankaccountv1.ListInternalBankAccountsRequest{
		AccountTypeFilter:    internalbankaccountv1.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
		InstrumentCodeFilter: instrumentCode,
		StatusFilter:         internalbankaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
	})
	if err != nil {
		r.logger.Error("failed to query internal bank accounts",
			"clearing_type", clearingType,
			"instrument_code", instrumentCode,
			"error", err)
		return "", fmt.Errorf("failed to query clearing account: %w", err)
	}

	if len(resp.Facilities) == 0 {
		r.logger.Warn("no active clearing account found",
			"clearing_type", clearingType,
			"instrument_code", instrumentCode)
		return "", fmt.Errorf("%w for %s %s", ErrNoClearingAccountFound, clearingType, instrumentCode)
	}

	// Use the first active clearing account found.
	// In the future, we could add logic to select based on additional criteria
	// (e.g., account name pattern for deposit vs withdrawal).
	account := resp.Facilities[0]
	return account.AccountId, nil
}

// cacheKey generates a cache key for the given clearing type and instrument.
func (r *AccountResolver) cacheKey(clearingType ClearingAccountType, instrumentCode string) string {
	return fmt.Sprintf("%s:%s", clearingType, instrumentCode)
}

// InvalidateCache clears all cached entries. Useful for testing or when accounts are modified.
func (r *AccountResolver) InvalidateCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = make(map[string]cacheEntry)
	r.logger.Debug("cache invalidated")
}

// InvalidateCacheEntry removes a specific entry from the cache.
func (r *AccountResolver) InvalidateCacheEntry(clearingType ClearingAccountType, instrumentCode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, r.cacheKey(clearingType, instrumentCode))
	r.logger.Debug("cache entry invalidated",
		"clearing_type", clearingType,
		"instrument_code", instrumentCode)
}
