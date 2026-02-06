package valuation

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrCacheMethodNil is returned when attempting to cache a nil method.
	ErrCacheMethodNil = errors.New("method cannot be nil")

	// ErrCachePolicyNil is returned when attempting to cache a nil policy.
	ErrCachePolicyNil = errors.New("policy cannot be nil")
)

// InMemoryCacheConfig holds configuration for the in-memory cache.
type InMemoryCacheConfig struct {
	// MethodTTL is the time-to-live for cached methods.
	// Default: 5 minutes.
	MethodTTL time.Duration

	// PolicyTTL is the time-to-live for cached policies.
	// Default: 5 minutes.
	PolicyTTL time.Duration
}

// inMemoryCache implements Cache using in-memory storage with TTL.
type inMemoryCache struct {
	methods      map[string]*methodCacheEntry
	policies     map[string]*policyCacheEntry
	methodsLock  sync.RWMutex
	policiesLock sync.RWMutex
	methodTTL    time.Duration
	policyTTL    time.Duration
}

type methodCacheEntry struct {
	method    *Method
	expiresAt time.Time
}

type policyCacheEntry struct {
	policy    CompiledPolicy
	expiresAt time.Time
}

// NewInMemoryCache creates a new in-memory cache with TTL support.
func NewInMemoryCache(cfg InMemoryCacheConfig) Cache {
	methodTTL := cfg.MethodTTL
	if methodTTL == 0 {
		methodTTL = 5 * time.Minute
	}

	policyTTL := cfg.PolicyTTL
	if policyTTL == 0 {
		policyTTL = 5 * time.Minute
	}

	return &inMemoryCache{
		methods:   make(map[string]*methodCacheEntry),
		policies:  make(map[string]*policyCacheEntry),
		methodTTL: methodTTL,
		policyTTL: policyTTL,
	}
}

// GetMethod retrieves a cached valuation method.
// Returns (nil, nil) for cache miss or expired entry.
func (c *inMemoryCache) GetMethod(methodID string, version *int) (*Method, error) {
	if version == nil {
		// Latest version lookup not yet implemented
		// Would require tracking version numbers per method ID
		return nil, nil //nolint:nilnil // cache miss returns (nil, nil) by design
	}

	key := c.methodKey(methodID, *version)

	c.methodsLock.RLock()
	entry, exists := c.methods[key]
	c.methodsLock.RUnlock()

	if !exists {
		return nil, nil //nolint:nilnil // cache miss returns (nil, nil) by design
	}

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		// Entry expired - remove it
		c.methodsLock.Lock()
		delete(c.methods, key)
		c.methodsLock.Unlock()
		return nil, nil //nolint:nilnil // expired entry treated as cache miss
	}

	return entry.method, nil
}

// SetMethod stores a valuation method in cache with TTL.
func (c *inMemoryCache) SetMethod(method *Method) error {
	if method == nil {
		return ErrCacheMethodNil
	}

	key := c.methodKey(method.ID, method.Version)
	entry := &methodCacheEntry{
		method:    method,
		expiresAt: time.Now().Add(c.methodTTL),
	}

	c.methodsLock.Lock()
	c.methods[key] = entry
	c.methodsLock.Unlock()

	return nil
}

// GetPolicy retrieves a cached CEL policy.
// Returns (nil, nil) for cache miss or expired entry.
func (c *inMemoryCache) GetPolicy(policyName string, version *int) (CompiledPolicy, error) {
	if version == nil {
		// Latest version lookup not yet implemented
		return nil, nil //nolint:nilnil // cache miss returns (nil, nil) by design
	}

	key := c.policyKey(policyName, *version)

	c.policiesLock.RLock()
	entry, exists := c.policies[key]
	c.policiesLock.RUnlock()

	if !exists {
		return nil, nil //nolint:nilnil // cache miss returns (nil, nil) by design
	}

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		// Entry expired - remove it
		c.policiesLock.Lock()
		delete(c.policies, key)
		c.policiesLock.Unlock()
		return nil, nil //nolint:nilnil // expired entry treated as cache miss
	}

	return entry.policy, nil
}

// SetPolicy stores a compiled policy in cache with TTL.
func (c *inMemoryCache) SetPolicy(policyName string, version int, policy CompiledPolicy) error {
	if policy == nil {
		return ErrCachePolicyNil
	}

	key := c.policyKey(policyName, version)
	entry := &policyCacheEntry{
		policy:    policy,
		expiresAt: time.Now().Add(c.policyTTL),
	}

	c.policiesLock.Lock()
	c.policies[key] = entry
	c.policiesLock.Unlock()

	return nil
}

// Clear removes all cached entries.
func (c *inMemoryCache) Clear() {
	c.methodsLock.Lock()
	c.methods = make(map[string]*methodCacheEntry)
	c.methodsLock.Unlock()

	c.policiesLock.Lock()
	c.policies = make(map[string]*policyCacheEntry)
	c.policiesLock.Unlock()
}

// methodKey generates a cache key for a method.
func (c *inMemoryCache) methodKey(methodID string, version int) string {
	return fmt.Sprintf("method:%s:v%d", methodID, version)
}

// policyKey generates a cache key for a policy.
func (c *inMemoryCache) policyKey(policyName string, version int) string {
	return fmt.Sprintf("policy:%s:v%d", policyName, version)
}
