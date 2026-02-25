// Package service implements gRPC services for the position keeping domain.
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
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
		lookupCtx, cancel := clients.WithTimeout(ctx, v.lookupTimeout)
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

// InternalAccountClient defines the interface for the Internal Account gRPC client.
// This abstraction allows for easy testing and decoupling from the generated client.
type InternalAccountClient interface {
	RetrieveInternalAccount(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error)
}

// InternalAccountValidator validates accounts using the Internal Account service.
// It implements the AccountValidator interface with caching and graceful degradation.
//
// Thread-safe: All methods can be called concurrently from multiple goroutines.
// Uses singleflight to prevent cache stampede (multiple concurrent requests for the same key).
type InternalAccountValidator struct {
	client InternalAccountClient
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

// InternalAccountValidatorConfig holds configuration for creating an InternalAccountValidator.
type InternalAccountValidatorConfig struct {
	// Client is the Internal Account gRPC client.
	Client InternalAccountClient

	// Logger is used for logging validator operations.
	Logger *slog.Logger

	// CacheTTL is how long validation results are cached.
	// Defaults to 1 minute if not specified.
	CacheTTL time.Duration

	// LookupTimeout is the timeout for individual lookup requests to the Internal Account service.
	// Defaults to 2 seconds if not specified.
	LookupTimeout time.Duration
}

// NewInternalAccountValidator creates a new InternalAccountValidator with the given configuration.
// Returns an error if required configuration is missing.
func NewInternalAccountValidator(cfg InternalAccountValidatorConfig) (*InternalAccountValidator, error) {
	if cfg.Client == nil {
		return nil, status.Error(codes.Internal, "internal account client cannot be nil")
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

	return &InternalAccountValidator{
		client:        cfg.Client,
		logger:        cfg.Logger,
		cacheTTL:      ttl,
		lookupTimeout: lookupTimeout,
		cache:         make(map[string]validationCacheEntry),
	}, nil
}

// ValidateExists checks if an account exists using the Internal Account service.
// Returns nil if the account exists or if the service is unavailable (graceful degradation).
// Returns codes.InvalidArgument if the account does not exist.
func (v *InternalAccountValidator) ValidateExists(ctx context.Context, accountID string) error {
	// Check cache first (read lock)
	v.mu.RLock()
	if entry, ok := v.cache[accountID]; ok && time.Now().Before(entry.expiresAt) {
		v.mu.RUnlock()
		v.logger.Debug("cache hit for internal account validation",
			"account_id", accountID,
			"exists", entry.exists)
		if !entry.exists {
			return status.Errorf(codes.InvalidArgument, "internal account not found: %s", accountID)
		}
		return nil
	}
	v.mu.RUnlock()

	// Use singleflight to coalesce concurrent requests for the same account.
	start := time.Now()
	result, err, shared := v.sfGroup.Do(accountID, func() (interface{}, error) {
		// Double-check cache after acquiring the singleflight "lock"
		v.mu.RLock()
		if entry, ok := v.cache[accountID]; ok && time.Now().Before(entry.expiresAt) {
			v.mu.RUnlock()
			return entry.exists, nil
		}
		v.mu.RUnlock()

		// Create a timeout context for the lookup to enable faster fallback
		lookupCtx, cancel := clients.WithTimeout(ctx, v.lookupTimeout)
		defer cancel()

		// Query the Internal Account service
		exists, lookupErr := v.queryInternalAccount(lookupCtx, accountID)
		if lookupErr != nil {
			// Graceful degradation: if service is unavailable, allow the operation
			v.logger.Warn("internal account service unavailable, skipping validation",
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
		v.logger.Error("unexpected error during internal account validation",
			"account_id", accountID,
			"error", err)
		return nil // Graceful degradation
	}

	exists, ok := result.(bool)
	if !ok {
		v.logger.Error("internal error: unexpected result type from singleflight",
			"account_id", accountID)
		return nil // Graceful degradation
	}

	v.logger.Debug("internal account validation completed",
		"account_id", accountID,
		"exists", exists,
		"duration_ms", time.Since(start).Milliseconds(),
		"shared", shared)

	if !exists {
		return status.Errorf(codes.InvalidArgument, "internal account not found: %s", accountID)
	}

	return nil
}

// queryInternalAccount queries the Internal Account service to check if an account exists.
// Returns (true, nil) if account exists, (false, nil) if not found, or (false, error) on service error.
func (v *InternalAccountValidator) queryInternalAccount(ctx context.Context, accountID string) (bool, error) {
	resp, err := v.client.RetrieveInternalAccount(ctx, &internalaccountv1.RetrieveInternalAccountRequest{
		AccountId: accountID,
	})
	if err != nil {
		// Check if it's a NotFound error - that means the account doesn't exist
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			v.logger.Debug("account not found in internal account service",
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

// CompositeAccountValidator validates accounts by checking multiple services.
// It tries each validator in order and returns success if any validator finds the account.
// This allows Position Keeping to validate accounts from both Current Account and Internal Account.
//
// Validation order: Current Account -> Internal Account
// - If found in Current Account: success
// - If NotFound in Current Account, check Internal Account
// - If found in Internal Account: success
// - If NotFound in both: return InvalidArgument error
// - If any service is unavailable: graceful degradation (allow operation)
type CompositeAccountValidator struct {
	currentAccountValidator  *CurrentAccountValidator
	internalAccountValidator *InternalAccountValidator
	logger                   *slog.Logger
}

// CompositeAccountValidatorConfig holds configuration for creating a CompositeAccountValidator.
type CompositeAccountValidatorConfig struct {
	// CurrentAccountValidator validates customer-facing accounts.
	// Optional - if nil, current account validation is skipped.
	CurrentAccountValidator *CurrentAccountValidator

	// InternalAccountValidator validates internal accounts.
	// Optional - if nil, internal account validation is skipped.
	InternalAccountValidator *InternalAccountValidator

	// Logger is used for logging validator operations.
	Logger *slog.Logger
}

// NewCompositeAccountValidator creates a new CompositeAccountValidator with the given configuration.
// At least one validator must be provided.
func NewCompositeAccountValidator(cfg CompositeAccountValidatorConfig) (*CompositeAccountValidator, error) {
	if cfg.CurrentAccountValidator == nil && cfg.InternalAccountValidator == nil {
		return nil, status.Error(codes.Internal, "at least one account validator must be provided")
	}
	if cfg.Logger == nil {
		return nil, ErrAccountValidatorLoggerNil
	}

	return &CompositeAccountValidator{
		currentAccountValidator:  cfg.CurrentAccountValidator,
		internalAccountValidator: cfg.InternalAccountValidator,
		logger:                   cfg.Logger,
	}, nil
}

// ValidateExists checks if an account exists by trying multiple services.
// Returns nil if the account exists in either Current Account or Internal Account.
// Returns codes.InvalidArgument if the account is not found in any service.
// Returns nil on service unavailability (graceful degradation).
func (v *CompositeAccountValidator) ValidateExists(ctx context.Context, accountID string) error {
	// Try Current Account first (most common case - customer accounts)
	if v.currentAccountValidator != nil {
		err := v.currentAccountValidator.ValidateExists(ctx, accountID)
		if err == nil {
			// Found in Current Account
			v.logger.Debug("account found in current account service",
				"account_id", accountID)
			return nil
		}

		// Check if it's an InvalidArgument (not found) vs other errors
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			// Not found in Current Account - try Internal Account
			v.logger.Debug("account not found in current account, trying internal account",
				"account_id", accountID)
		} else {
			// Other error (service unavailable) - graceful degradation already handled
			return nil
		}
	}

	// Try Internal Account
	if v.internalAccountValidator != nil {
		err := v.internalAccountValidator.ValidateExists(ctx, accountID)
		if err == nil {
			// Found in Internal Account
			v.logger.Debug("account found in internal account service",
				"account_id", accountID)
			return nil
		}

		// Check if it's an InvalidArgument (not found)
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			// Not found in Internal Account either
			v.logger.Debug("account not found in internal account service",
				"account_id", accountID)
		} else {
			// Other error (service unavailable) - graceful degradation already handled
			return nil
		}
	}

	// Account not found in any service
	v.logger.Warn("account not found in any account service",
		"account_id", accountID)
	return status.Errorf(codes.InvalidArgument, "account not found: %s", accountID)
}
