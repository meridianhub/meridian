// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"golang.org/x/sync/singleflight"
)

// AccountResolverErrors defines sentinel errors for the AccountResolver.
var (
	// ErrNoClearingAccountFound is returned when no active clearing account is found for the given criteria.
	ErrNoClearingAccountFound = errors.New("no active clearing account found")

	// ErrAccountResolverClientNil is returned when attempting to create an AccountResolver with a nil client.
	ErrAccountResolverClientNil = errors.New("internal account client cannot be nil")

	// ErrAccountResolverLoggerNil is returned when attempting to create an AccountResolver with a nil logger.
	ErrAccountResolverLoggerNil = errors.New("logger cannot be nil")

	// ErrAccountResolverInternalError is returned when an unexpected internal error occurs.
	ErrAccountResolverInternalError = errors.New("internal error: unexpected result type from singleflight")
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

// AccountResolver resolves clearing account IDs dynamically from the Internal Account service.
// It provides caching to reduce external service calls and supports both deposit and withdrawal
// clearing account lookups.
//
// Thread-safe: All methods can be called concurrently from multiple goroutines.
// Uses singleflight to prevent cache stampede (multiple concurrent requests for the same key).
type AccountResolver struct {
	client InternalAccountClient
	logger *slog.Logger

	// Cache configuration
	cacheTTL      time.Duration
	lookupTimeout time.Duration

	// Thread-safe cache: key is "TYPE:INSTRUMENT" (e.g., "DEPOSIT:GBP")
	mu    sync.RWMutex
	cache map[string]cacheEntry

	// Singleflight to coalesce concurrent requests for the same cache key
	sfGroup singleflight.Group
}

// AccountResolverConfig holds configuration for creating an AccountResolver.
type AccountResolverConfig struct {
	// Client is the Internal Account gRPC client.
	Client InternalAccountClient

	// Logger is used for logging resolver operations.
	Logger *slog.Logger

	// CacheTTL is how long resolved account IDs are cached.
	// Defaults to 5 minutes if not specified.
	CacheTTL time.Duration

	// LookupTimeout is the timeout for individual lookup requests to the Internal Account service.
	// Defaults to 2 seconds if not specified.
	LookupTimeout time.Duration
}

const (
	// DefaultCacheTTL is the default cache TTL for resolved account IDs.
	DefaultCacheTTL = 5 * time.Minute

	// DefaultLookupTimeout is the default timeout for clearing account lookups.
	// This is shorter than the typical RPC timeout to allow graceful fallback to static config.
	DefaultLookupTimeout = 2 * time.Second
)

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

	lookupTimeout := cfg.LookupTimeout
	if lookupTimeout == 0 {
		lookupTimeout = DefaultLookupTimeout
	}

	return &AccountResolver{
		client:        cfg.Client,
		logger:        cfg.Logger,
		cacheTTL:      ttl,
		lookupTimeout: lookupTimeout,
		cache:         make(map[string]cacheEntry),
	}, nil
}

// GetDepositClearingAccount resolves the clearing account ID for deposit operations
// for the given instrument (currency/asset code like "GBP", "USD", "KWH").
//
// It first checks the cache, and if not found or expired, queries the Internal Account
// service for an active CLEARING account matching the instrument.
func (r *AccountResolver) GetDepositClearingAccount(ctx context.Context, instrumentCode string) (string, error) {
	return r.resolveClearingAccount(ctx, ClearingAccountTypeDeposit, instrumentCode)
}

// GetWithdrawalClearingAccount resolves the clearing account ID for withdrawal operations
// for the given instrument (currency/asset code like "GBP", "USD", "KWH").
//
// It first checks the cache, and if not found or expired, queries the Internal Account
// service for an active CLEARING account matching the instrument.
func (r *AccountResolver) GetWithdrawalClearingAccount(ctx context.Context, instrumentCode string) (string, error) {
	return r.resolveClearingAccount(ctx, ClearingAccountTypeWithdrawal, instrumentCode)
}

// resolveClearingAccount is the internal implementation for resolving clearing accounts.
// Uses singleflight to prevent cache stampede when multiple concurrent requests miss the cache.
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

	// Use singleflight to coalesce concurrent requests for the same cache key.
	// This prevents cache stampede when multiple goroutines miss the cache simultaneously.
	start := time.Now()
	result, err, shared := r.sfGroup.Do(cacheKey, func() (interface{}, error) {
		// Double-check cache after acquiring the singleflight "lock"
		// Another goroutine might have already populated the cache
		r.mu.RLock()
		if entry, ok := r.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
			r.mu.RUnlock()
			return entry.accountID, nil
		}
		r.mu.RUnlock()

		// Create a timeout context for the lookup to enable faster fallback
		lookupCtx, cancel := context.WithTimeout(ctx, r.lookupTimeout)
		defer cancel()

		// Query the Internal Account service
		accountID, err := r.queryInternalAccount(lookupCtx, clearingType, instrumentCode)
		if err != nil {
			return "", err
		}

		// Cache the result (write lock)
		r.mu.Lock()
		r.cache[cacheKey] = cacheEntry{
			accountID: accountID,
			expiresAt: time.Now().Add(r.cacheTTL),
		}
		r.mu.Unlock()

		return accountID, nil
	})

	if err != nil {
		caobservability.RecordClearingAccountLookupError(string(clearingType))
		return "", err
	}

	accountID, ok := result.(string)
	if !ok {
		// This should never happen as we control the singleflight function return type
		return "", ErrAccountResolverInternalError
	}
	caobservability.RecordClearingAccountLookupDuration(time.Since(start))

	r.logger.Info("resolved clearing account",
		"clearing_type", clearingType,
		"instrument_code", instrumentCode,
		"account_id", accountID,
		"duration_ms", time.Since(start).Milliseconds(),
		"shared", shared)

	return accountID, nil
}

// queryInternalAccount queries the Internal Account service for a clearing account.
//
// Note: clearingType is currently used only for logging and cache key generation.
// The Internal Account API doesn't yet support filtering by clearing purpose
// (deposit vs withdrawal), so the same account is returned for both operations.
// Cache keys include clearingType intentionally to support future differentiation
// without cache invalidation when the API is extended.
func (r *AccountResolver) queryInternalAccount(ctx context.Context, clearingType ClearingAccountType, instrumentCode string) (string, error) {
	resp, err := r.client.ListInternalAccounts(ctx, &internalaccountv1.ListInternalAccountsRequest{
		BehaviorClassFilter:  "CLEARING",
		InstrumentCodeFilter: instrumentCode,
		StatusFilter:         internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
	})
	if err != nil {
		r.logger.Error("failed to query internal accounts",
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
	// The Internal Account API does not currently support clearing purpose filtering,
	// so a single clearing account handles both deposit and withdrawal operations.
	// This is acceptable for initial deployment.
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
