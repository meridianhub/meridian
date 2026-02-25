// Package session provides session-scoped state for the MCP server,
// including plan caching and request rate limiting.
package session

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// planEntry holds a cached plan with its expiration time.
type planEntry struct {
	expiresAt time.Time
}

// PlanCache stores manifest hashes with TTL-based expiry.
// It enforces the plan-before-apply workflow: a plan must be stored
// before an apply operation referencing that plan hash is permitted.
type PlanCache struct {
	mu      sync.Mutex
	entries map[string]planEntry
	ttl     time.Duration
}

// NewPlanCache returns a PlanCache with the given TTL for each stored plan.
// Panics if ttl is non-positive to prevent silent misconfiguration where
// entries would be immediately invalid.
func NewPlanCache(ttl time.Duration) *PlanCache {
	if ttl <= 0 {
		panic("session: PlanCache TTL must be positive")
	}
	return &PlanCache{
		entries: make(map[string]planEntry),
		ttl:     ttl,
	}
}

// Store hashes the manifest with SHA256, records it with the configured TTL,
// and returns the hex-encoded hash. Storing the same manifest again resets its TTL.
func (c *PlanCache) Store(manifest []byte) string {
	hash := hashManifest(manifest)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[hash] = planEntry{expiresAt: time.Now().Add(c.ttl)}
	return hash
}

// Exists returns true when the hash is present and has not expired.
func (c *PlanCache) Exists(hash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[hash]
	if !ok {
		return false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, hash)
		return false
	}
	return true
}

// Cleanup removes all expired entries from the cache.
func (c *PlanCache) Cleanup() {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for hash, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, hash)
		}
	}
}

// hashManifest returns the SHA256 hex digest of the manifest bytes.
func hashManifest(manifest []byte) string {
	sum := sha256.Sum256(manifest)
	return hex.EncodeToString(sum[:])
}
