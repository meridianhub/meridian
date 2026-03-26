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
type TenantInfoHandler struct {
	logger   *slog.Logger
	limiters sync.Map // IP -> *rate.Limiter
}

// NewTenantInfoHandler creates a handler for the public tenant info endpoint.
func NewTenantInfoHandler(logger *slog.Logger) *TenantInfoHandler {
	return &TenantInfoHandler{logger: logger}
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
		limiter := h.getLimiter(ip)
		if !limiter.Allow() {
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

// getLimiter returns the rate limiter for the given IP, creating one if needed.
func (h *TenantInfoHandler) getLimiter(ip string) *rate.Limiter {
	if v, ok := h.limiters.Load(ip); ok {
		return v.(*rate.Limiter)
	}
	// 30 requests/minute = 0.5/second, burst of 10
	limiter := rate.NewLimiter(rate.Every(2*time.Second), 10)
	actual, _ := h.limiters.LoadOrStore(ip, limiter)
	return actual.(*rate.Limiter)
}

// WithTenantInfoHandler sets the tenant info handler for the server.
func WithTenantInfoHandler(handler *TenantInfoHandler) ServerOption {
	return func(s *Server) {
		s.tenantInfoHandler = handler
	}
}
