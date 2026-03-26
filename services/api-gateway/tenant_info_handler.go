package gateway

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"golang.org/x/time/rate"
)

// TenantInfoHandler serves public tenant metadata for the login page.
// The per-IP rate limiter map is bounded by periodic eviction of idle entries.
type TenantInfoHandler struct {
	logger   *slog.Logger
	mu       sync.Mutex
	limiters map[string]*ipRateLimiter
	stop     chan struct{}
	stopOnce sync.Once
}

type ipRateLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewTenantInfoHandler creates a handler for the public tenant info endpoint.
// A background goroutine evicts idle rate limiter entries every 10 minutes.
func NewTenantInfoHandler(logger *slog.Logger) *TenantInfoHandler {
	h := &TenantInfoHandler{
		logger:   logger,
		limiters: make(map[string]*ipRateLimiter),
		stop:     make(chan struct{}),
	}
	go h.cleanupLoop()
	return h
}

// Stop halts the background cleanup goroutine. Safe to call multiple times.
func (h *TenantInfoHandler) Stop() {
	h.stopOnce.Do(func() { close(h.stop) })
}

// tenantInfoResponse is the JSON body returned by GET /api/tenant-info.
type tenantInfoResponse struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
}

// HandleTenantInfo returns an http.HandlerFunc for GET /api/tenant-info.
// It reads tenant context injected by the tenant resolver middleware and
// returns the slug and display name as JSON.
func (h *TenantInfoHandler) HandleTenantInfo() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// Per-IP rate limiting: 30 requests per minute with burst of 10.
		ip := getClientIP(r)
		if !h.allow(ip) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		ctx := r.Context()
		slug, slugOk := tenant.SlugFromContext(ctx)
		if !slugOk || slug == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "tenant not found",
			})
			return
		}

		displayName, _ := tenant.DisplayNameFromContext(ctx)

		w.Header().Set("Cache-Control", "public, s-maxage=300")
		writeJSON(w, http.StatusOK, tenantInfoResponse{
			Slug:        slug,
			DisplayName: displayName,
		})
	}
}

// allow checks whether the IP is permitted to make a request.
func (h *TenantInfoHandler) allow(ip string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	il, ok := h.limiters[ip]
	if !ok {
		// 30 requests/minute = 0.5/second, burst of 10
		il = &ipRateLimiter{
			limiter: rate.NewLimiter(rate.Every(2*time.Second), 10),
		}
		h.limiters[ip] = il
	}
	il.lastSeen = time.Now()
	return il.limiter.Allow()
}

// cleanupLoop evicts rate limiter entries idle for more than 10 minutes.
func (h *TenantInfoHandler) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.cleanup(10 * time.Minute)
		case <-h.stop:
			return
		}
	}
}

// cleanup removes entries that have not been seen for the given duration.
func (h *TenantInfoHandler) cleanup(maxAge time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for ip, il := range h.limiters {
		if il.lastSeen.Before(cutoff) {
			delete(h.limiters, ip)
		}
	}
}

// WithTenantInfoHandler sets the tenant info handler for the server.
func WithTenantInfoHandler(handler *TenantInfoHandler) ServerOption {
	return func(s *Server) {
		s.tenantInfoHandler = handler
	}
}
