package gateway

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IPRateLimiter provides per-IP token-bucket rate limiting for unauthenticated
// endpoints. It keeps an in-memory map of token-bucket limiters keyed by client
// IP and is intended to be checked before any expensive work (e.g. a database
// lookup) so that abusive callers are rejected cheaply.
//
// Limitation: state is per-process. With multiple replicas, each has an
// independent limiter, so the effective limit is multiplied by replica count.
// For stronger guarantees, replace with a Redis-backed limiter
// (e.g., go-redis/redis_rate).
type IPRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipRateEntry
	rate     rate.Limit
	burst    int
	stop     chan struct{}
	stopOnce sync.Once
}

type ipRateEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewIPRateLimiter creates a per-IP rate limiter with the given sustained rate
// and burst size. A background goroutine runs every hour to evict stale entries.
func NewIPRateLimiter(r rate.Limit, burst int) *IPRateLimiter {
	if burst < 1 {
		burst = 1
	}
	rl := &IPRateLimiter{
		limiters: make(map[string]*ipRateEntry),
		rate:     r,
		burst:    burst,
		stop:     make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// NewPerMinuteIPRateLimiter creates a per-IP rate limiter that sustains
// perMinute requests per minute per IP with the given burst allowance. It is a
// convenience wrapper around NewIPRateLimiter for the common per-minute case.
func NewPerMinuteIPRateLimiter(perMinute, burst int) *IPRateLimiter {
	if perMinute < 1 {
		perMinute = 1
	}
	return NewIPRateLimiter(rate.Limit(float64(perMinute)/60.0), burst)
}

// resolveIPRateLimiter returns the configured limiter when non-nil, otherwise a
// new per-minute limiter built from the supplied defaults. It centralizes the
// "use the injected limiter or fall back to a default" logic shared by the
// unauthenticated handlers.
func resolveIPRateLimiter(configured *IPRateLimiter, defaultPerMinute, defaultBurst int) *IPRateLimiter {
	if configured != nil {
		return configured
	}
	return NewPerMinuteIPRateLimiter(defaultPerMinute, defaultBurst)
}

// Allow reports whether the given IP is permitted to make a request now,
// consuming one token from its bucket when permitted.
func (rl *IPRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.limiters[ip]
	if !ok {
		entry = &ipRateEntry{
			limiter: rate.NewLimiter(rl.rate, rl.burst),
		}
		rl.limiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

// Cleanup removes entries that have not been seen for the given duration.
func (rl *IPRateLimiter) Cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for ip, entry := range rl.limiters {
		if entry.lastSeen.Before(cutoff) {
			delete(rl.limiters, ip)
		}
	}
}

// Stop halts the background cleanup goroutine. It is safe to call more than once.
func (rl *IPRateLimiter) Stop() {
	rl.stopOnce.Do(func() {
		close(rl.stop)
	})
}

// cleanupLoop evicts stale limiter entries every hour.
func (rl *IPRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.Cleanup(time.Hour)
		case <-rl.stop:
			return
		}
	}
}
