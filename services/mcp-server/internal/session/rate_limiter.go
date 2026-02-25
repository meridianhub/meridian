package session

import (
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// CategoryLimit defines the maximum number of requests allowed within a time window
// for a specific tool category.
type CategoryLimit struct {
	MaxRequests int
	Window      time.Duration
}

// windowState tracks the fixed-window counter for a single category.
type windowState struct {
	count       int
	windowStart time.Time
}

// RateLimiter enforces per-category fixed-window rate limits.
// Each category maintains an independent counter that resets when its window expires.
type RateLimiter struct {
	mu     sync.Mutex
	limits map[tools.ToolCategory]CategoryLimit
	state  map[tools.ToolCategory]*windowState
}

// NewRateLimiter returns a RateLimiter with the given per-category limits.
// Categories not present in limits are always allowed.
// Panics if any limit has a non-positive Window or non-positive MaxRequests,
// to prevent accidental unlimited-request bypass through misconfiguration.
func NewRateLimiter(limits map[tools.ToolCategory]CategoryLimit) *RateLimiter {
	copiedLimits := make(map[tools.ToolCategory]CategoryLimit, len(limits))
	state := make(map[tools.ToolCategory]*windowState, len(limits))
	now := time.Now()
	for cat, limit := range limits {
		if limit.Window <= 0 {
			panic("session: RateLimiter Window must be positive")
		}
		if limit.MaxRequests <= 0 {
			panic("session: RateLimiter MaxRequests must be positive")
		}
		copiedLimits[cat] = limit
		state[cat] = &windowState{windowStart: now}
	}
	return &RateLimiter{
		limits: copiedLimits,
		state:  state,
	}
}

// DefaultLimits returns the standard rate limits for the MCP server:
//   - Read:     60 requests/minute
//   - Simulate: 30 requests/minute
//   - Write:    5 requests/minute
func DefaultLimits() map[tools.ToolCategory]CategoryLimit {
	return map[tools.ToolCategory]CategoryLimit{
		tools.CategoryRead:     {MaxRequests: 60, Window: time.Minute},
		tools.CategorySimulate: {MaxRequests: 30, Window: time.Minute},
		tools.CategoryWrite:    {MaxRequests: 5, Window: time.Minute},
	}
}

// Allow returns true and increments the counter when the category is within its limit.
// Returns false when the limit has been exceeded for the current window.
// Categories without a configured limit always return true.
func (r *RateLimiter) Allow(category tools.ToolCategory) bool {
	limit, ok := r.limits[category]
	if !ok {
		return true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	ws, ok := r.state[category]
	if !ok {
		ws = &windowState{windowStart: time.Now()}
		r.state[category] = ws
	}

	now := time.Now()
	if now.Sub(ws.windowStart) >= limit.Window {
		ws.windowStart = now
		ws.count = 0
	}

	if ws.count >= limit.MaxRequests {
		return false
	}

	ws.count++
	return true
}
