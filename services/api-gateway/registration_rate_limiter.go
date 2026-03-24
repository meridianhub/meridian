package gateway

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RegistrationRateLimiter provides per-IP rate limiting for the registration endpoint.
// It uses an in-memory map of token-bucket limiters keyed by client IP.
//
// Limitation: state is per-process. With multiple replicas, each has an independent
// limiter, so the effective limit is multiplied by replica count. For stronger
// guarantees, replace with a Redis-backed limiter (e.g., go-redis/redis_rate).
type RegistrationRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipLimiter
	rate     rate.Limit
	burst    int
	stop     chan struct{}
}

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRegistrationRateLimiter creates a rate limiter that allows the given number of
// registrations per 24-hour window per IP address. A background goroutine runs
// every hour to evict stale entries.
func NewRegistrationRateLimiter(perDay int) *RegistrationRateLimiter {
	rl := &RegistrationRateLimiter{
		limiters: make(map[string]*ipLimiter),
		rate:     rate.Limit(float64(perDay) / (24 * 60 * 60)),
		burst:    perDay,
		stop:     make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
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

// Stop halts the background cleanup goroutine.
func (rl *RegistrationRateLimiter) Stop() {
	close(rl.stop)
}

// cleanupLoop evicts stale limiter entries every hour.
func (rl *RegistrationRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.Cleanup(24 * time.Hour)
		case <-rl.stop:
			return
		}
	}
}
