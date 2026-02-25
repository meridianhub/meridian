package session

import (
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// Config holds configuration for a Session.
type Config struct {
	// PlanTTL is the duration a stored plan hash remains valid.
	PlanTTL time.Duration
	// Limits defines per-category rate limits.
	Limits map[tools.ToolCategory]CategoryLimit
}

// Session combines plan caching and rate limiting for a single MCP session.
// It is safe for concurrent use.
type Session struct {
	cache   *PlanCache
	limiter *RateLimiter
}

// New returns a Session configured with the provided Config.
func New(cfg Config) *Session {
	return &Session{
		cache:   NewPlanCache(cfg.PlanTTL),
		limiter: NewRateLimiter(cfg.Limits),
	}
}

// NewDefault returns a Session with production-ready defaults:
//   - Plan TTL: 30 minutes
//   - Rate limits: Read 60/min, Simulate 30/min, Write 5/min
func NewDefault() *Session {
	return New(Config{
		PlanTTL: 30 * time.Minute,
		Limits:  DefaultLimits(),
	})
}

// StorePlan hashes the manifest and records it in the plan cache.
// Returns the hex-encoded SHA256 hash that must be provided to ValidatePlan.
func (s *Session) StorePlan(manifest []byte) string {
	return s.cache.Store(manifest)
}

// ValidatePlan returns true when a plan with the given hash exists and has not expired.
// This enforces the plan-before-apply workflow.
func (s *Session) ValidatePlan(hash string) bool {
	return s.cache.Exists(hash)
}

// Allow returns true when the category has capacity within its rate limit window.
func (s *Session) Allow(category tools.ToolCategory) bool {
	return s.limiter.Allow(category)
}

// Cleanup removes expired plan cache entries. Call periodically to reclaim memory.
func (s *Session) Cleanup() {
	s.cache.Cleanup()
}
