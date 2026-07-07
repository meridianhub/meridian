package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// apiKeyIndexSecret is a per-process random secret used to derive opaque cache
// and rate-limiter indices from API keys via HMAC. It never leaves the process
// and is regenerated on each start; the cache and limiters are in-memory only,
// so a fresh secret per process is fine.
var apiKeyIndexSecret = mustRandomBytes(32)

func mustRandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: failed to initialize API-key index secret: %v", err))
	}
	return b
}

// apiKeyCacheIndex derives an opaque, non-reversible index for the full API key
// using HMAC-SHA-256 under a per-process secret.
//
// The validation cache and per-key rate limiters are keyed on this index so
// that entries are scoped to the exact full key without retaining the plaintext
// key in memory. Keying on the truncated prefix (pk_{slug}_{first 8 entropy
// chars}) would let a forged key that shares a legitimate key's prefix read the
// legitimate key's cached validation result - authenticating on the prefix
// alone - or poison it with a negative result. This is a keyed MAC used purely
// as an in-memory lookup index, not a password hash, and is never persisted or
// logged.
func apiKeyCacheIndex(apiKey string) string {
	mac := hmac.New(sha256.New, apiKeyIndexSecret)
	mac.Write([]byte(apiKey))
	return hex.EncodeToString(mac.Sum(nil))
}

// ErrNotPrefixedKey is returned when the API key is not in the pk_{slug}_{entropy} format.
// Callers should fall back to legacy API key validation.
var ErrNotPrefixedKey = errors.New("not a prefixed API key")

// APIKeyValidationResult holds the cached result of a Control Plane validation.
type APIKeyValidationResult struct {
	Valid        bool
	TenantID     string
	Identity     string
	Scopes       []string
	RateLimitRPS int32
}

// SlugResolver resolves a tenant slug to a tenant ID.
// This allows the gateway to use its existing tenant registry cache.
type SlugResolver interface {
	ResolveSlug(ctx context.Context, slug string) (tenant.TenantID, error)
}

// RPCAPIKeyValidator validates API keys by calling the Control Plane AuthService.
// It parses the pk_{slug}_{entropy} format, resolves tenant slugs, and caches results.
type RPCAPIKeyValidator struct {
	client       controlplanev1.AuthServiceClient
	slugResolver SlugResolver
	logger       *slog.Logger

	// Cache for validation results
	cache    sync.Map // map[string]*cachedValidation
	cacheTTL time.Duration

	// Per-key rate limiters (keyed by an HMAC index of the full key, see apiKeyCacheIndex)
	limiters  sync.Map // map[string]*rpcRateLimiterEntry
	stopCh    chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once

	// Cleanup configuration
	cleanupInterval    time.Duration
	limiterIdleTimeout time.Duration
}

type cachedValidation struct {
	result    *APIKeyValidationResult
	expiresAt time.Time
}

type rpcRateLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
	mu         sync.Mutex
}

// RPCValidatorConfig configures the RPCAPIKeyValidator.
type RPCValidatorConfig struct {
	// Client is the gRPC client for the Control Plane AuthService.
	Client controlplanev1.AuthServiceClient

	// SlugResolver resolves tenant slugs to tenant IDs.
	SlugResolver SlugResolver

	// CacheTTL is the TTL for cached validation results (default: 5 minutes).
	CacheTTL time.Duration

	// CleanupInterval is how often to clean up expired cache and limiter entries (default: 1 minute).
	CleanupInterval time.Duration

	// LimiterIdleTimeout is how long a rate limiter can be idle before cleanup (default: 10 minutes).
	LimiterIdleTimeout time.Duration

	// Logger for logging.
	Logger *slog.Logger
}

// NewRPCAPIKeyValidator creates a new RPC-based API key validator.
func NewRPCAPIKeyValidator(config RPCValidatorConfig) *RPCAPIKeyValidator {
	if config.CacheTTL <= 0 {
		config.CacheTTL = 5 * time.Minute
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 1 * time.Minute
	}
	if config.LimiterIdleTimeout <= 0 {
		config.LimiterIdleTimeout = 10 * time.Minute
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	v := &RPCAPIKeyValidator{
		client:             config.Client,
		slugResolver:       config.SlugResolver,
		logger:             config.Logger,
		cacheTTL:           config.CacheTTL,
		cleanupInterval:    config.CleanupInterval,
		limiterIdleTimeout: config.LimiterIdleTimeout,
		stopCh:             make(chan struct{}),
	}

	v.wg.Add(1)
	go v.cleanupLoop()

	return v
}

// Close stops the background cleanup goroutine.
func (v *RPCAPIKeyValidator) Close() {
	v.closeOnce.Do(func() {
		close(v.stopCh)
		v.wg.Wait()
	})
}

// ParsePrefixedKey parses a pk_{slug}_{entropy} formatted API key.
// Returns the tenant slug and full key prefix, or empty strings if the format is invalid.
func ParsePrefixedKey(apiKey string) (tenantSlug, keyPrefix string, ok bool) {
	// Format: pk_{slug}_{entropy}
	// The key prefix is: pk_{slug}_{first8chars of entropy}
	if !strings.HasPrefix(apiKey, "pk_") {
		return "", "", false
	}

	// Remove "pk_" prefix
	rest := apiKey[3:]

	// Find the second underscore (after slug)
	underscoreIdx := strings.Index(rest, "_")
	if underscoreIdx <= 0 {
		return "", "", false
	}

	slug := rest[:underscoreIdx]
	if slug == "" {
		return "", "", false
	}

	entropy := rest[underscoreIdx+1:]
	if len(entropy) < 8 {
		return "", "", false
	}

	// Key prefix is pk_{slug}_{first 8 chars of entropy}
	prefix := fmt.Sprintf("pk_%s_%s", slug, entropy[:8])

	return slug, prefix, true
}

// Validate validates an API key via the Control Plane RPC.
// Returns the validation result if the key is in pk_{slug}_{...} format and valid.
// Returns ErrNotPrefixedKey if the key format is not recognized (caller should fall back to legacy).
func (v *RPCAPIKeyValidator) Validate(ctx context.Context, apiKey string) (*APIKeyValidationResult, error) {
	slug, keyPrefix, ok := ParsePrefixedKey(apiKey)
	if !ok {
		return nil, ErrNotPrefixedKey
	}

	// Cache is keyed on an HMAC index of the full key, not the truncated prefix,
	// so a forged key that shares a legitimate key's prefix cannot read (or
	// poison) the legitimate key's cached result. The index binds to the exact
	// full key without retaining the plaintext key in memory; entries are held
	// in process memory only and expire after cacheTTL.
	cacheKey := apiKeyCacheIndex(apiKey)

	// Check cache first
	if cached := v.getCached(cacheKey); cached != nil {
		return cached, nil
	}

	// Resolve slug -> tenant_id
	tenantID, err := v.slugResolver.ResolveSlug(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant slug %q: %w", slug, err)
	}

	// Call Control Plane ValidateAPIKey RPC with tenant context
	// The tenant ID is passed via gRPC metadata so the server can set search_path
	md := metadata.Pairs("x-tenant-id", tenantID.String())
	rpcCtx := metadata.NewOutgoingContext(ctx, md)

	resp, err := v.client.ValidateAPIKey(rpcCtx, &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    keyPrefix,
		PlaintextKey: apiKey,
	}, grpc.WaitForReady(false))
	if err != nil {
		return nil, fmt.Errorf("validate API key via control plane: %w", err)
	}

	result := &APIKeyValidationResult{
		Valid:        resp.GetValid(),
		TenantID:     resp.GetTenantId(),
		Identity:     resp.GetIdentity(),
		Scopes:       resp.GetScopes(),
		RateLimitRPS: resp.GetRateLimitRps(),
	}

	// Cache the result (even if invalid, to prevent brute force). The negative
	// cache entry is scoped to the exact full key's index, so it cannot affect
	// any other key that happens to share this key's prefix.
	v.setCache(cacheKey, result)

	return result, nil
}

// AllowRequest checks if the given rate-limit key is within its rate limit.
// The rate-limit key should be the HMAC index of the full key (see
// apiKeyCacheIndex) so that two distinct keys sharing a truncated prefix do not
// share a rate-limit bucket.
func (v *RPCAPIKeyValidator) AllowRequest(rateKey string, rateLimitRPS int32) bool {
	if rateLimitRPS <= 0 {
		rateLimitRPS = 100 // default
	}

	now := time.Now()

	value, loaded := v.limiters.Load(rateKey)
	if loaded {
		entry, ok := value.(*rpcRateLimiterEntry)
		if !ok {
			return false
		}
		entry.mu.Lock()
		entry.lastAccess = now
		entry.mu.Unlock()
		return entry.limiter.Allow()
	}

	limiter := rate.NewLimiter(rate.Limit(rateLimitRPS), int(rateLimitRPS)*2)
	entry := &rpcRateLimiterEntry{
		limiter:    limiter,
		lastAccess: now,
	}

	actual, loaded := v.limiters.LoadOrStore(rateKey, entry)
	if loaded {
		existingEntry, ok := actual.(*rpcRateLimiterEntry)
		if !ok {
			return false
		}
		existingEntry.mu.Lock()
		existingEntry.lastAccess = now
		existingEntry.mu.Unlock()
		return existingEntry.limiter.Allow()
	}

	return entry.limiter.Allow()
}

func (v *RPCAPIKeyValidator) getCached(keyPrefix string) *APIKeyValidationResult {
	value, ok := v.cache.Load(keyPrefix)
	if !ok {
		return nil
	}

	cached, ok := value.(*cachedValidation)
	if !ok {
		return nil
	}

	if time.Now().After(cached.expiresAt) {
		v.cache.Delete(keyPrefix)
		return nil
	}

	return cached.result
}

func (v *RPCAPIKeyValidator) setCache(keyPrefix string, result *APIKeyValidationResult) {
	v.cache.Store(keyPrefix, &cachedValidation{
		result:    result,
		expiresAt: time.Now().Add(v.cacheTTL),
	})
}

func (v *RPCAPIKeyValidator) cleanupLoop() {
	defer v.wg.Done()

	ticker := time.NewTicker(v.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-v.stopCh:
			return
		case <-ticker.C:
			v.cleanupExpired()
		}
	}
}

func (v *RPCAPIKeyValidator) cleanupExpired() {
	now := time.Now()

	// Clean up expired cache entries
	v.cache.Range(func(key, value interface{}) bool {
		cached, ok := value.(*cachedValidation)
		if !ok || now.After(cached.expiresAt) {
			v.cache.Delete(key)
		}
		return true
	})

	// Clean up idle rate limiters
	v.limiters.Range(func(key, value interface{}) bool {
		entry, ok := value.(*rpcRateLimiterEntry)
		if !ok {
			return true
		}
		entry.mu.Lock()
		idle := now.Sub(entry.lastAccess) > v.limiterIdleTimeout
		entry.mu.Unlock()
		if idle {
			v.limiters.Delete(key)
		}
		return true
	})
}
