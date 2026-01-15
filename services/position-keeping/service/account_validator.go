// Package service implements gRPC services for the position keeping domain.
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccountValidator validates that accounts exist before creating position logs.
// This interface allows Position Keeping to validate account existence without
// direct coupling to the Current Account implementation details.
type AccountValidator interface {
	// ValidateExists checks if an account exists and is valid for position tracking.
	// Returns nil if the account exists and is valid.
	// Returns codes.InvalidArgument error if the account does not exist.
	// Returns nil on service unavailability (graceful degradation).
	ValidateExists(ctx context.Context, accountID string) error
}

// AccountValidatorErrors defines sentinel errors for the AccountValidator.
var (
	// ErrAccountValidatorClientNil is returned when attempting to create an AccountValidator with a nil client.
	ErrAccountValidatorClientNil = status.Error(codes.Internal, "current account client cannot be nil")

	// ErrAccountValidatorLoggerNil is returned when attempting to create an AccountValidator with a nil logger.
	ErrAccountValidatorLoggerNil = status.Error(codes.Internal, "logger cannot be nil")
)

// CurrentAccountClient defines the interface for the Current Account gRPC client.
// This abstraction allows for easy testing and decoupling from the generated client.
type CurrentAccountClient interface {
	RetrieveCurrentAccount(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error)
}

// validationCacheEntry holds a cached validation result with its expiration time.
type validationCacheEntry struct {
	exists    bool
	expiresAt time.Time
}

// CurrentAccountValidator validates accounts using the Current Account service.
// It implements the AccountValidator interface with caching and graceful degradation.
//
// Thread-safe: All methods can be called concurrently from multiple goroutines.
// Uses singleflight to prevent cache stampede (multiple concurrent requests for the same key).
type CurrentAccountValidator struct {
	client CurrentAccountClient
	logger *slog.Logger

	// Cache configuration
	cacheTTL      time.Duration
	lookupTimeout time.Duration

	// Thread-safe cache: key is accountID
	mu    sync.RWMutex
	cache map[string]validationCacheEntry

	// Singleflight to coalesce concurrent requests for the same account
	sfGroup singleflight.Group
}

// CurrentAccountValidatorConfig holds configuration for creating a CurrentAccountValidator.
type CurrentAccountValidatorConfig struct {
	// Client is the Current Account gRPC client.
	Client CurrentAccountClient

	// Logger is used for logging validator operations.
	Logger *slog.Logger

	// CacheTTL is how long validation results are cached.
	// Defaults to 1 minute if not specified.
	CacheTTL time.Duration

	// LookupTimeout is the timeout for individual lookup requests to the Current Account service.
	// Defaults to 2 seconds if not specified.
	LookupTimeout time.Duration
}

const (
	// DefaultValidationCacheTTL is the default cache TTL for validation results.
	DefaultValidationCacheTTL = 1 * time.Minute

	// DefaultValidationLookupTimeout is the default timeout for account validation lookups.
	// This is shorter than the typical RPC timeout to allow graceful fallback.
	DefaultValidationLookupTimeout = 2 * time.Second
)

// NewCurrentAccountValidator creates a new CurrentAccountValidator with the given configuration.
// Returns an error if required configuration is missing.
func NewCurrentAccountValidator(cfg CurrentAccountValidatorConfig) (*CurrentAccountValidator, error) {
	if cfg.Client == nil {
		return nil, ErrAccountValidatorClientNil
	}
	if cfg.Logger == nil {
		return nil, ErrAccountValidatorLoggerNil
	}

	ttl := cfg.CacheTTL
	if ttl == 0 {
		ttl = DefaultValidationCacheTTL
	}

	lookupTimeout := cfg.LookupTimeout
	if lookupTimeout == 0 {
		lookupTimeout = DefaultValidationLookupTimeout
	}

	return &CurrentAccountValidator{
		client:        cfg.Client,
		logger:        cfg.Logger,
		cacheTTL:      ttl,
		lookupTimeout: lookupTimeout,
		cache:         make(map[string]validationCacheEntry),
	}, nil
}

// ValidateExists checks if an account exists using the Current Account service.
// Returns nil if the account exists or if the service is unavailable (graceful degradation).
// Returns codes.InvalidArgument if the account does not exist.
func (v *CurrentAccountValidator) ValidateExists(ctx context.Context, accountID string) error {
	// Check cache first (read lock)
	v.mu.RLock()
	if entry, ok := v.cache[accountID]; ok && time.Now().Before(entry.expiresAt) {
		v.mu.RUnlock()
		v.logger.Debug("cache hit for account validation",
			"account_id", accountID,
			"exists", entry.exists)
		if !entry.exists {
			return status.Errorf(codes.InvalidArgument, "account not found: %s", accountID)
		}
		return nil
	}
	v.mu.RUnlock()

	// Use singleflight to coalesce concurrent requests for the same account.
	// This prevents cache stampede when multiple goroutines validate the same account simultaneously.
	start := time.Now()
	result, err, shared := v.sfGroup.Do(accountID, func() (interface{}, error) {
		// Double-check cache after acquiring the singleflight "lock"
		// Another goroutine might have already populated the cache
		v.mu.RLock()
		if entry, ok := v.cache[accountID]; ok && time.Now().Before(entry.expiresAt) {
			v.mu.RUnlock()
			return entry.exists, nil
		}
		v.mu.RUnlock()

		// Create a timeout context for the lookup to enable faster fallback
		lookupCtx, cancel := context.WithTimeout(ctx, v.lookupTimeout)
		defer cancel()

		// Query the Current Account service
		exists, lookupErr := v.queryCurrentAccount(lookupCtx, accountID)
		if lookupErr != nil {
			// Graceful degradation: if service is unavailable, allow the operation
			v.logger.Warn("current account service unavailable, skipping validation",
				"account_id", accountID,
				"error", lookupErr)
			return true, nil // Return true to allow operation
		}

		// Cache the result (write lock)
		v.mu.Lock()
		v.cache[accountID] = validationCacheEntry{
			exists:    exists,
			expiresAt: time.Now().Add(v.cacheTTL),
		}
		v.mu.Unlock()

		return exists, nil
	})

	if err != nil {
		// This shouldn't happen with our implementation, but handle it gracefully
		v.logger.Error("unexpected error during account validation",
			"account_id", accountID,
			"error", err)
		return nil // Graceful degradation
	}

	exists, ok := result.(bool)
	if !ok {
		// This should never happen as we control the singleflight function return type
		v.logger.Error("internal error: unexpected result type from singleflight",
			"account_id", accountID)
		return nil // Graceful degradation
	}

	v.logger.Debug("account validation completed",
		"account_id", accountID,
		"exists", exists,
		"duration_ms", time.Since(start).Milliseconds(),
		"shared", shared)

	if !exists {
		return status.Errorf(codes.InvalidArgument, "account not found: %s", accountID)
	}

	return nil
}

// queryCurrentAccount queries the Current Account service to check if an account exists.
// Returns (true, nil) if account exists, (false, nil) if not found, or (false, error) on service error.
func (v *CurrentAccountValidator) queryCurrentAccount(ctx context.Context, accountID string) (bool, error) {
	resp, err := v.client.RetrieveCurrentAccount(ctx, &currentaccountv1.RetrieveCurrentAccountRequest{
		AccountId: accountID,
	})
	if err != nil {
		// Check if it's a NotFound error - that means the account doesn't exist
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			v.logger.Debug("account not found in current account service",
				"account_id", accountID)
			return false, nil
		}
		// Any other error is a service availability issue
		return false, err
	}

	// Account exists if we got a valid response with a facility
	exists := resp != nil && resp.Facility != nil
	return exists, nil
}

// InvalidateCache clears all cached entries. Useful for testing or when accounts are modified.
func (v *CurrentAccountValidator) InvalidateCache() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cache = make(map[string]validationCacheEntry)
	v.logger.Debug("validation cache invalidated")
}

// InvalidateCacheEntry removes a specific entry from the cache.
func (v *CurrentAccountValidator) InvalidateCacheEntry(accountID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.cache, accountID)
	v.logger.Debug("validation cache entry invalidated",
		"account_id", accountID)
}
