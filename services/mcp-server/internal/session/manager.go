package session

import (
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

const (
	// managerEvictInterval is how often Manager sweeps idle per-key limiters.
	managerEvictInterval = 10 * time.Minute
	// managerIdleTTL is how long a key's limiter survives without activity
	// before eviction, bounding memory growth over a long-running server's
	// lifetime as distinct tenants/sessions come and go.
	managerIdleTTL = 30 * time.Minute
)

// keyedLimiter pairs a RateLimiter with its last-used time so evictIdle can
// reclaim limiters for tenants/sessions that have gone quiet.
type keyedLimiter struct {
	limiter    *RateLimiter
	lastUsedAt time.Time
}

// Manager scopes rate limiting per key — typically an authenticated tenant
// ID or an MCP session ID — so that one caller's traffic can never exhaust
// another caller's quota. Each key gets its own independent RateLimiter,
// created lazily on first use with the same configured limits. Satisfies
// tools.RateLimiter. Safe for concurrent use.
type Manager struct {
	mu        sync.Mutex
	limits    map[tools.ToolCategory]CategoryLimit
	limiters  map[string]*keyedLimiter
	stop      chan struct{}
	closeOnce sync.Once
}

// NewManager returns a Manager that enforces limits independently per key,
// and starts a background goroutine that evicts idle keys. Call Close when
// the manager is no longer needed to stop that goroutine.
func NewManager(limits map[tools.ToolCategory]CategoryLimit) *Manager {
	copiedLimits := make(map[tools.ToolCategory]CategoryLimit, len(limits))
	for cat, limit := range limits {
		copiedLimits[cat] = limit
	}
	m := &Manager{
		limits:   copiedLimits,
		limiters: make(map[string]*keyedLimiter),
		stop:     make(chan struct{}),
	}
	go m.evictLoop()
	return m
}

// Close stops the background eviction goroutine. Safe to call more than once.
func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.stop) })
}

// Allow reports whether the caller scoped to key has capacity remaining for
// category, consulting (and lazily creating) that key's own RateLimiter.
// Keys are fully independent: exhausting one key's limit never affects any
// other key.
func (m *Manager) Allow(key string, category tools.ToolCategory) bool {
	m.mu.Lock()
	kl, ok := m.limiters[key]
	if !ok {
		kl = &keyedLimiter{limiter: NewRateLimiter(m.limits)}
		m.limiters[key] = kl
	}
	kl.lastUsedAt = time.Now()
	m.mu.Unlock()

	return kl.limiter.Allow(category)
}

func (m *Manager) evictLoop() {
	ticker := time.NewTicker(managerEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.evictIdle()
		case <-m.stop:
			return
		}
	}
}

func (m *Manager) evictIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, kl := range m.limiters {
		if time.Since(kl.lastUsedAt) > managerIdleTTL {
			delete(m.limiters, key)
		}
	}
}
