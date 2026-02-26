// Package middleware provides HTTP middleware for the gateway service.
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/gateway/auth"
	"github.com/meridianhub/meridian/services/gateway/internal/mapping"
)

// HeaderIdempotencyKey is the header name for the derived idempotency key.
const HeaderIdempotencyKey = "Idempotency-Key"

// HeaderMappingVersion is the header name for requesting a specific mapping version.
const HeaderMappingVersion = "X-Mapping-Version"

// MappingResolver resolves a MappingDefinition by name for a given tenant.
// Implementations may cache results or call an RPC backend directly.
type MappingResolver interface {
	// Resolve returns the latest ACTIVE mapping definition for the given name and tenant.
	// Returns nil and ErrMappingNotFound if no active mapping exists.
	Resolve(ctx context.Context, tenantID, name string) (*mappingv1.MappingDefinition, error)
}

var (
	// ErrMappingNotFound is returned when no active mapping is found for the given name.
	ErrMappingNotFound = errors.New("mapping not found")

	// ErrNoTenantID is returned when the request has no tenant ID in context.
	ErrNoTenantID = errors.New("tenant ID not found in request context")

	// ErrInvalidMappingName is returned when the mapping name in the URL is empty or nested.
	ErrInvalidMappingName = errors.New("invalid mapping name")

	// ErrEmptyBody is returned when the request body is empty.
	ErrEmptyBody = errors.New("request body is empty")

	// ErrInvalidMappingTarget is returned when a mapping has empty target_service or target_rpc.
	ErrInvalidMappingTarget = errors.New("mapping has empty target_service or target_rpc")
)

// MappingMiddleware intercepts requests to /mapping/{name}, resolves the mapping
// definition, applies inbound transformation, and rewrites the request URL and
// body before forwarding to the next handler (Vanguard transcoder).
type MappingMiddleware struct {
	resolver MappingResolver
	engine   *mapping.Engine
	logger   *slog.Logger
}

// NewMappingMiddleware creates a MappingMiddleware with the given resolver and engine.
func NewMappingMiddleware(resolver MappingResolver, engine *mapping.Engine, logger *slog.Logger) *MappingMiddleware {
	return &MappingMiddleware{
		resolver: resolver,
		engine:   engine,
		logger:   logger,
	}
}

// Handler wraps the given handler with mapping middleware.
// Requests matching /mapping/{name} are intercepted and transformed.
// All other requests pass through unchanged.
func (m *MappingMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only intercept paths starting with /mapping/
		if !strings.HasPrefix(r.URL.Path, "/mapping/") {
			next.ServeHTTP(w, r)
			return
		}

		if err := m.handleMappingRequest(w, r, next); err != nil {
			m.logger.Debug("mapping request handled with error", "error", err)
		}
	})
}

// handleMappingRequest processes a /mapping/{name} request. It resolves the
// mapping definition, transforms the body, rewrites the URL, and forwards to next.
// Returns an error for logging only; HTTP errors are already written to w.
func (m *MappingMiddleware) handleMappingRequest(w http.ResponseWriter, r *http.Request, next http.Handler) error {
	// Extract mapping name from URL path: /mapping/{name}
	name := strings.TrimPrefix(r.URL.Path, "/mapping/")
	if name == "" || strings.Contains(name, "/") {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "mapping name must be a single path segment")
		return ErrInvalidMappingName
	}

	// Extract tenant ID from auth context
	tenantID, ok := auth.GetTenantIDFromContext(r.Context())
	if !ok || tenantID == "" {
		writeJSONError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "tenant ID not found in request context")
		return ErrNoTenantID
	}

	// Resolve mapping definition
	mappingDef, err := m.resolveMappingDef(r.Context(), w, tenantID, name)
	if err != nil {
		return err
	}

	// Read and transform body
	result, err := m.readAndTransform(w, r, mappingDef, tenantID, name)
	if err != nil {
		return err
	}

	// Set idempotency key header if derived
	if result.IdempotencyKey != "" {
		r.Header.Set(HeaderIdempotencyKey, result.IdempotencyKey)
	}

	// Replace request body with transformed proto JSON
	r.Body = io.NopCloser(bytes.NewReader(result.ProtoJSON))
	r.ContentLength = int64(len(result.ProtoJSON))

	// Validate target fields before rewriting URL
	targetService := mappingDef.GetTargetService()
	targetRPC := mappingDef.GetTargetRpc()
	if targetService == "" || targetRPC == "" {
		writeJSONError(w, http.StatusBadGateway, "INTERNAL",
			"mapping definition has empty target_service or target_rpc")
		return ErrInvalidMappingTarget
	}

	// Rewrite URL from /mapping/{name} to the target gRPC-style path:
	// /{service}/{rpc}
	targetPath := "/" + targetService + "/" + targetRPC
	r.URL.Path = targetPath

	m.logger.Debug("mapping middleware applied",
		"name", name,
		"tenant_id", tenantID,
		"target_path", targetPath,
		"idempotency_key", result.IdempotencyKey != "")

	// Intercept the response from Vanguard to apply outbound transformation.
	rec := newResponseRecorder()
	defer rec.release()

	downstreamStart := time.Now()
	next.ServeHTTP(rec, r)
	downstreamElapsed := time.Since(downstreamStart)

	// Pass non-2xx responses through untransformed.
	if rec.code < 200 || rec.code >= 300 {
		return writeSanitizedResponse(w, rec.code, rec.headers, rec.buf.Bytes())
	}

	// If the response body is empty, pass it through without transformation.
	responseBody := rec.buf.Bytes()
	if len(responseBody) == 0 {
		copyHeaders(w.Header(), rec.headers)
		w.WriteHeader(rec.code)
		return nil
	}

	// Apply outbound transformation to the proto-JSON response body.
	transformStart := time.Now()
	transformed, err := m.engine.TransformOutbound(mappingDef, responseBody)
	transformElapsed := time.Since(transformStart)
	if err != nil {
		m.logger.Error("outbound transformation failed",
			"name", name,
			"tenant_id", tenantID,
			"mapping_version", mappingDef.GetVersion(),
			"downstream_elapsed", downstreamElapsed,
			"transform_elapsed", transformElapsed,
			"error", err)
		writeJSONError(w, http.StatusInternalServerError, "INTERNAL", "outbound transformation failed")
		return err
	}

	m.logger.Debug("outbound transformation applied",
		"name", name,
		"tenant_id", tenantID,
		"downstream_elapsed", downstreamElapsed,
		"transform_elapsed", transformElapsed,
		"input_bytes", rec.buf.Len(),
		"output_bytes", len(transformed))

	return writeSanitizedResponse(w, rec.code, rec.headers, transformed)
}

// writeSanitizedResponse validates body as JSON via sanitizeJSON, copies
// downstream headers, removes stale body-dependent headers, and writes the
// sanitized response. Returns an error if the body is not valid JSON.
func writeSanitizedResponse(w http.ResponseWriter, code int, srcHeaders http.Header, body []byte) error {
	sanitized, err := sanitizeJSON(body)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "BAD_GATEWAY", "response body is not valid JSON")
		return err
	}
	copyHeaders(w.Header(), srcHeaders)
	setSafeResponseHeaders(w)
	w.Header().Del("Transfer-Encoding")
	w.Header().Del("Content-Encoding")
	w.Header().Del("ETag")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(sanitized)))
	w.WriteHeader(code)
	_, _ = w.Write(sanitized)
	return nil
}

// sanitizeJSON decodes and re-encodes JSON to break CodeQL's taint-tracking
// chain from user-controlled request data to http.ResponseWriter.Write.
// The decode/re-encode cycle creates new Go values, producing untainted output.
// json.Marshal also HTML-escapes <, >, & in string values for XSS safety.
// Returns an error if the input is not valid JSON.
func sanitizeJSON(data []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// setSafeResponseHeaders sets Content-Type and X-Content-Type-Options to prevent
// browsers from interpreting JSON API responses as HTML (reflected XSS mitigation).
func setSafeResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// copyHeaders copies all headers from src into dst.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// resolveMappingDef resolves a mapping definition by name and tenant, writing
// appropriate HTTP errors on failure.
func (m *MappingMiddleware) resolveMappingDef(ctx context.Context, w http.ResponseWriter, tenantID, name string) (*mappingv1.MappingDefinition, error) {
	mappingDef, err := m.resolver.Resolve(ctx, tenantID, name)
	if err != nil {
		if errors.Is(err, ErrMappingNotFound) {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND",
				fmt.Sprintf("no active mapping found for name %q", name))
			return nil, err
		}
		m.logger.Error("failed to resolve mapping",
			"name", name,
			"tenant_id", tenantID,
			"error", err)
		writeJSONError(w, http.StatusBadGateway, "UNAVAILABLE", "failed to resolve mapping definition")
		return nil, err
	}
	return mappingDef, nil
}

// readAndTransform reads the request body and applies the inbound transformation.
func (m *MappingMiddleware) readAndTransform(w http.ResponseWriter, r *http.Request, mappingDef *mappingv1.MappingDefinition, tenantID, name string) (*mapping.InboundResult, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read request body")
		return nil, err
	}
	_ = r.Body.Close()

	if len(body) == 0 {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is empty")
		return nil, ErrEmptyBody
	}

	result, err := m.engine.TransformInbound(mappingDef, body)
	if err != nil {
		m.logger.Warn("inbound transformation failed",
			"name", name,
			"tenant_id", tenantID,
			"error", err)
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			fmt.Sprintf("inbound transformation failed: %v", err))
		return nil, err
	}
	return result, nil
}

func writeJSONError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	resp := struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}{
		Error: message,
		Code:  code,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// cacheEntry holds a cached mapping definition with an expiration time.
type cacheEntry struct {
	mapping   *mappingv1.MappingDefinition
	expiresAt time.Time
}

// CachedMappingResolver wraps a MappingResolver with an in-memory TTL cache.
// It uses a sync.Map for concurrent access and lazy expiration.
type CachedMappingResolver struct {
	delegate MappingResolver
	ttl      time.Duration
	cache    sync.Map // map[string]*cacheEntry (key: "tenantID:name")
}

// NewCachedMappingResolver creates a CachedMappingResolver with the given TTL.
func NewCachedMappingResolver(delegate MappingResolver, ttl time.Duration) *CachedMappingResolver {
	return &CachedMappingResolver{
		delegate: delegate,
		ttl:      ttl,
	}
}

// Resolve looks up the mapping definition by name and tenant, returning a
// cached result if available and not expired. On cache miss or expiry, it
// calls the delegate resolver and caches the result.
func (c *CachedMappingResolver) Resolve(ctx context.Context, tenantID, name string) (*mappingv1.MappingDefinition, error) {
	key := cacheKey(tenantID, name)

	// Check cache
	if val, ok := c.cache.Load(key); ok {
		entry, valid := val.(*cacheEntry)
		if valid && time.Now().Before(entry.expiresAt) {
			return entry.mapping, nil
		}
		// Expired or invalid — fall through to delegate
		c.cache.Delete(key)
	}

	// Resolve from delegate
	md, err := c.delegate.Resolve(ctx, tenantID, name)
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.cache.Store(key, &cacheEntry{
		mapping:   md,
		expiresAt: time.Now().Add(c.ttl),
	})

	return md, nil
}

// Invalidate removes a cached entry for the given tenant and name.
func (c *CachedMappingResolver) Invalidate(tenantID, name string) {
	c.cache.Delete(cacheKey(tenantID, name))
}

// cacheKey builds a collision-resistant cache key using a null byte separator.
// Tenant IDs are UUIDs and mapping names are alphanumeric, so neither can
// contain a null byte.
func cacheKey(tenantID, name string) string {
	return tenantID + "\x00" + name
}

// GRPCMappingResolver resolves mapping definitions by calling the MappingService gRPC client.
// It uses ListMappings with status=ACTIVE to find mappings by name.
type GRPCMappingResolver struct {
	client mappingv1.MappingServiceClient
}

// NewGRPCMappingResolver creates a resolver backed by the MappingService gRPC client.
func NewGRPCMappingResolver(client mappingv1.MappingServiceClient) *GRPCMappingResolver {
	return &GRPCMappingResolver{client: client}
}

// Resolve calls ListMappings for ACTIVE mappings and finds one matching the given name.
// The tenantID is not passed directly to the RPC; it is propagated via gRPC metadata
// headers set by the gateway's metadata propagation middleware on the outgoing context.
func (g *GRPCMappingResolver) Resolve(ctx context.Context, _ string, name string) (*mappingv1.MappingDefinition, error) {
	resp, err := g.client.ListMappings(ctx, &mappingv1.ListMappingsRequest{
		Status: mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
	})
	if err != nil {
		return nil, fmt.Errorf("listing mappings: %w", err)
	}

	// Find the mapping with the matching name (latest version wins)
	var best *mappingv1.MappingDefinition
	for _, md := range resp.GetMappings() {
		if md.GetName() == name {
			if best == nil || md.GetVersion() > best.GetVersion() {
				best = md
			}
		}
	}

	if best == nil {
		return nil, ErrMappingNotFound
	}

	return best, nil
}
