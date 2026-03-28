// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	"golang.org/x/sync/singleflight"
)

// AccountResolverErrors defines sentinel errors for the AccountResolver.
var (
	// ErrNoClearingAccountFound is returned when no active clearing account is found for the given criteria.
	ErrNoClearingAccountFound = errors.New("no active clearing account found")

	// ErrMultipleClearingAccounts is returned when multiple active clearing accounts are found for the same criteria.
	// This indicates a data inconsistency - each instrument/purpose combination should have exactly one active account.
	ErrMultipleClearingAccounts = errors.New("multiple active clearing accounts found")

	// ErrEmptyClearingAccountID is returned when a clearing account has an empty account_id.
	ErrEmptyClearingAccountID = errors.New("clearing account has empty account_id")

	// ErrNilClearingAccountResponse is returned when the internal account service returns a nil response.
	ErrNilClearingAccountResponse = errors.New("nil response from internal account service")

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
	// ClearingAccountTypeSettlement identifies the clearing account for settlement operations.
	// Payment Order uses settlement accounts for payment completion and reconciliation
	// with external payment gateways.
	ClearingAccountTypeSettlement ClearingAccountType = "SETTLEMENT"
)

// cacheEntry holds a cached account ID with its expiration time.
type cacheEntry struct {
	accountID string
	expiresAt time.Time
}

// AccountResolver resolves clearing account IDs dynamically from the Internal Account service.
// It provides caching to reduce external service calls and supports settlement clearing account lookups.
//
// Thread-safe: All methods can be called concurrently from multiple goroutines.
// Uses singleflight to prevent cache stampede (multiple concurrent requests for the same key).
type AccountResolver struct {
	client InternalAccountClient
	logger *slog.Logger

	// Cache configuration
	cacheTTL      time.Duration
	lookupTimeout time.Duration

	// Thread-safe cache: key is "TYPE:INSTRUMENT" (e.g., "SETTLEMENT:GBP")
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

// GetSettlementClearingAccount resolves the clearing account ID for settlement operations
// for the given instrument (currency/asset code like "GBP", "USD", "KWH").
//
// Settlement accounts are used by Payment Order for payment completion and reconciliation
// with external payment gateways.
//
// It first checks the cache, and if not found or expired, queries the Internal Account
// service for an active CLEARING account matching the instrument.
func (r *AccountResolver) GetSettlementClearingAccount(ctx context.Context, instrumentCode string) (string, error) {
	return r.resolveClearingAccount(ctx, ClearingAccountTypeSettlement, instrumentCode)
}

// resolveClearingAccount is the internal implementation for resolving clearing accounts.
// Uses singleflight to prevent cache stampede when multiple concurrent requests miss the cache.
func (r *AccountResolver) resolveClearingAccount(ctx context.Context, clearingType ClearingAccountType, instrumentCode string) (string, error) {
	cacheKey := r.cacheKey(clearingType, instrumentCode)

	if accountID, ok := r.checkCache(cacheKey, clearingType, instrumentCode); ok {
		return accountID, nil
	}

	poobservability.RecordClearingAccountCacheMiss()

	start := time.Now()
	result, err, shared := r.sfGroup.Do(cacheKey, func() (interface{}, error) {
		return r.fetchAndCacheClearingAccount(ctx, cacheKey, clearingType, instrumentCode)
	})

	if err != nil {
		poobservability.RecordClearingAccountLookupError(string(clearingType))
		return "", err
	}

	accountID, ok := result.(string)
	if !ok {
		return "", ErrAccountResolverInternalError
	}

	if !shared {
		poobservability.RecordClearingAccountLookupDuration(time.Since(start))
	}

	r.logger.Info("resolved clearing account",
		"clearing_type", clearingType,
		"instrument_code", instrumentCode,
		"account_id", accountID,
		"duration_ms", time.Since(start).Milliseconds(),
		"shared", shared)

	return accountID, nil
}

// checkCache checks the in-memory cache for a clearing account entry.
func (r *AccountResolver) checkCache(cacheKey string, clearingType ClearingAccountType, instrumentCode string) (string, bool) {
	r.mu.RLock()
	entry, ok := r.cache[cacheKey]
	r.mu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {
		r.logger.Debug("cache hit for clearing account",
			"clearing_type", clearingType,
			"instrument_code", instrumentCode,
			"account_id", entry.accountID)
		poobservability.RecordClearingAccountCacheHit()
		return entry.accountID, true
	}
	return "", false
}

// fetchAndCacheClearingAccount queries the Internal Account service and caches the result.
// Called within singleflight to prevent concurrent lookups for the same key.
func (r *AccountResolver) fetchAndCacheClearingAccount(ctx context.Context, cacheKey string, clearingType ClearingAccountType, instrumentCode string) (string, error) {
	// Double-check cache after acquiring the singleflight "lock"
	r.mu.RLock()
	if entry, ok := r.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		r.mu.RUnlock()
		return entry.accountID, nil
	}
	r.mu.RUnlock()

	lookupCtx, cancel := context.WithTimeout(ctx, r.lookupTimeout)
	defer cancel()

	accountID, err := r.queryInternalAccount(lookupCtx, clearingType, instrumentCode)
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	r.cache[cacheKey] = cacheEntry{
		accountID: accountID,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	r.mu.Unlock()

	return accountID, nil
}

// queryInternalAccount queries the Internal Account service for a clearing account
// with the specified clearing purpose.
func (r *AccountResolver) queryInternalAccount(ctx context.Context, clearingType ClearingAccountType, instrumentCode string) (string, error) {
	clearingPurpose := mapClearingTypeToPurpose(clearingType)

	resp, err := r.client.ListInternalAccounts(ctx, &internalaccountv1.ListInternalAccountsRequest{
		BehaviorClassFilter:   "CLEARING",
		InstrumentCodeFilter:  instrumentCode,
		StatusFilter:          internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
		ClearingPurposeFilter: clearingPurpose,
	})
	if err != nil {
		r.logger.Error("failed to query internal accounts",
			"clearing_type", clearingType,
			"clearing_purpose", clearingPurpose.String(),
			"instrument_code", instrumentCode,
			"error", err)
		return "", fmt.Errorf("failed to query clearing account: %w", err)
	}

	// Defensive check for nil response
	if resp == nil {
		r.logger.Error("nil response from internal account service",
			"clearing_type", clearingType,
			"clearing_purpose", clearingPurpose.String(),
			"instrument_code", instrumentCode)
		return "", fmt.Errorf("%w for %s %s", ErrNilClearingAccountResponse, clearingType, instrumentCode)
	}

	if len(resp.Facilities) == 0 {
		r.logger.Warn("no active clearing account found",
			"clearing_type", clearingType,
			"clearing_purpose", clearingPurpose.String(),
			"instrument_code", instrumentCode)
		return "", fmt.Errorf("%w for %s %s", ErrNoClearingAccountFound, clearingType, instrumentCode)
	}

	// Fail fast on multiple results to prevent nondeterministic routing.
	// Each instrument/purpose combination should have exactly one active clearing account.
	if len(resp.Facilities) > 1 {
		r.logger.Error("multiple active clearing accounts found - data inconsistency",
			"clearing_type", clearingType,
			"clearing_purpose", clearingPurpose.String(),
			"instrument_code", instrumentCode,
			"count", len(resp.Facilities))
		return "", fmt.Errorf("%w for %s %s (count: %d)", ErrMultipleClearingAccounts, clearingType, instrumentCode, len(resp.Facilities))
	}

	// Use the single active clearing account matching the criteria.
	account := resp.Facilities[0]

	// Defensive check for empty account_id
	if account.AccountId == "" {
		r.logger.Error("clearing account has empty account_id",
			"clearing_type", clearingType,
			"clearing_purpose", clearingPurpose.String(),
			"instrument_code", instrumentCode)
		return "", fmt.Errorf("%w for %s %s", ErrEmptyClearingAccountID, clearingType, instrumentCode)
	}

	return account.AccountId, nil
}

// mapClearingTypeToPurpose converts the internal ClearingAccountType to the proto ClearingPurpose enum.
func mapClearingTypeToPurpose(clearingType ClearingAccountType) internalaccountv1.ClearingPurpose {
	switch clearingType {
	case ClearingAccountTypeSettlement:
		return internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT
	default:
		// For unknown types, return UNSPECIFIED which means no filtering by clearing purpose.
		return internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED
	}
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
