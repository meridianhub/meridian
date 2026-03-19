package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPHandler_LivenessHandler tests the liveness endpoint
func TestHTTPHandler_LivenessHandler(t *testing.T) {
	agg := NewAggregator(nil)
	handler := NewHTTPHandler(agg)

	req := httptest.NewRequest("GET", "/health/live", nil)
	w := httptest.NewRecorder()

	handler.LivenessHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var response LivenessResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "alive" {
		t.Errorf("Status = %v, want alive", response.Status)
	}
}

// TestHTTPHandler_StartupHandler tests the startup endpoint
func TestHTTPHandler_StartupHandler(t *testing.T) {
	agg := NewAggregator(nil)
	handler := NewHTTPHandler(agg)

	req := httptest.NewRequest("GET", "/health/startup", nil)
	w := httptest.NewRecorder()

	handler.StartupHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var response LivenessResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "started" {
		t.Errorf("Status = %v, want started", response.Status)
	}
}

// TestHTTPHandler_ReadinessHandler_Healthy tests readiness with all components healthy
func TestHTTPHandler_ReadinessHandler_Healthy(t *testing.T) {
	checkers := []Checker{
		&mockChecker{name: "database", returnStatus: StatusHealthy},
		&mockChecker{name: "redis", returnStatus: StatusHealthy},
	}

	agg := NewAggregator(checkers)
	handler := NewHTTPHandler(agg)

	req := httptest.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()

	handler.ReadinessHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var response ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "healthy" {
		t.Errorf("Status = %v, want healthy", response.Status)
	}

	if len(response.Components) != 2 {
		t.Errorf("Components count = %d, want 2", len(response.Components))
	}
}

// TestHTTPHandler_ReadinessHandler_Unhealthy tests readiness with unhealthy component
func TestHTTPHandler_ReadinessHandler_Unhealthy(t *testing.T) {
	checkers := []Checker{
		&mockChecker{name: "database", returnStatus: StatusHealthy},
		&mockChecker{name: "redis", returnStatus: StatusUnhealthy, returnError: context.DeadlineExceeded},
	}

	agg := NewAggregator(checkers)
	handler := NewHTTPHandler(agg)

	req := httptest.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()

	handler.ReadinessHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var response ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "unhealthy" {
		t.Errorf("Status = %v, want unhealthy", response.Status)
	}

	// Find the redis component
	var redisComp *ComponentStatusJSON
	for i := range response.Components {
		if response.Components[i].Name == "redis" {
			redisComp = &response.Components[i]
			break
		}
	}

	if redisComp == nil {
		t.Fatal("Redis component not found in response")
		return // unreachable but satisfies staticcheck
	}

	if redisComp.Status != "unhealthy" {
		t.Errorf("Redis status = %v, want unhealthy", redisComp.Status)
	}

	if redisComp.Error == "" {
		t.Error("Redis error should not be empty")
	}
}

// TestHTTPHandler_ReadinessHandler_Degraded tests readiness with degraded component
func TestHTTPHandler_ReadinessHandler_Degraded(t *testing.T) {
	checkers := []Checker{
		&mockChecker{name: "database", returnStatus: StatusHealthy},
		&mockChecker{name: "kafka", returnStatus: StatusDegraded},
	}

	agg := NewAggregator(checkers)
	handler := NewHTTPHandler(agg)

	req := httptest.NewRequest("GET", "/health/ready", nil)
	w := httptest.NewRecorder()

	handler.ReadinessHandler(w, req)

	// Degraded should return 503 (not ready)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var response ReadinessResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Status != "degraded" {
		t.Errorf("Status = %v, want degraded", response.Status)
	}
}

// TestHTTPHandler_RegisterHandlers tests handler registration
func TestHTTPHandler_RegisterHandlers(t *testing.T) {
	mux := http.NewServeMux()
	agg := NewAggregator(nil)
	handler := NewHTTPHandler(agg)

	handler.RegisterHandlers(mux)

	// Test that all endpoints are registered
	endpoints := []string{
		"/health/live",
		"/health/ready",
		"/health/startup",
	}

	for _, endpoint := range endpoints {
		req := httptest.NewRequest("GET", endpoint, nil)
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: Status = %d, want 200 or 503", endpoint, w.Code)
		}
	}
}

// TestHTTPHandler_ContentType tests that responses have correct Content-Type
func TestHTTPHandler_ContentType(t *testing.T) {
	agg := NewAggregator(nil)
	handler := NewHTTPHandler(agg)

	endpoints := []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/health/live", handler.LivenessHandler},
		{"/health/ready", handler.ReadinessHandler},
		{"/health/startup", handler.StartupHandler},
	}

	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", ep.path, nil)
			w := httptest.NewRecorder()

			ep.handler(w, req)

			contentType := w.Header().Get("Content-Type")
			if contentType != "application/json" {
				t.Errorf("Content-Type = %v, want application/json", contentType)
			}
		})
	}
}
