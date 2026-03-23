package gateway

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RegistrationRateLimiter provides per-IP rate limiting for the registration endpoint.
// It uses an in-memory map of token-bucket limiters keyed by client IP.
type RegistrationRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipLimiter
	rate     rate.Limit
	burst    int
}

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRegistrationRateLimiter creates a rate limiter that allows the given number of
// registrations per 24-hour window per IP address.
func NewRegistrationRateLimiter(perDay int) *RegistrationRateLimiter {
	return &RegistrationRateLimiter{
		limiters: make(map[string]*ipLimiter),
		rate:     rate.Limit(float64(perDay) / (24 * 60 * 60)),
		burst:    perDay,
	}
}

// Allow returns true if the given IP is permitted to make a registration request.
func (rl *RegistrationRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	il, ok := rl.limiters[ip]
	if !ok {
		il = &ipLimiter{
			limiter: rate.NewLimiter(rl.rate, rl.burst),
		}
		rl.limiters[ip] = il
	}
	il.lastSeen = time.Now()
	return il.limiter.Allow()
}

// Cleanup removes entries that have not been seen for the given duration.
// Callers should invoke this periodically (e.g., every hour) to bound memory.
func (rl *RegistrationRateLimiter) Cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for ip, il := range rl.limiters {
		if il.lastSeen.Before(cutoff) {
			delete(rl.limiters, ip)
		}
	}
}

// ClientIP extracts the client IP from the request, preferring X-Forwarded-For
// when present (common behind reverse proxies), falling back to RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For: client, proxy1, proxy2 - take the leftmost (client).
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	// RemoteAddr is "host:port"; strip the port.
	addr := r.RemoteAddr
	if idx := strings.LastIndexByte(addr, ':'); idx > 0 {
		return addr[:idx]
	}
	return addr
}
