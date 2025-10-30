package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthLiveEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	w := httptest.NewRecorder()

	http.DefaultServeMux = http.NewServeMux()
	setupRoutes()

	http.DefaultServeMux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "alive\n" {
		t.Errorf("Expected body 'alive\\n', got %q", w.Body.String())
	}
}

func TestHealthReadyEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	w := httptest.NewRecorder()

	http.DefaultServeMux = http.NewServeMux()
	setupRoutes()

	http.DefaultServeMux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "ready\n" {
		t.Errorf("Expected body 'ready\\n', got %q", w.Body.String())
	}
}

func TestHealthStartupEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/startup", nil)
	w := httptest.NewRecorder()

	http.DefaultServeMux = http.NewServeMux()
	setupRoutes()

	http.DefaultServeMux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "started\n" {
		t.Errorf("Expected body 'started\\n', got %q", w.Body.String())
	}
}

func TestRootEndpoint(t *testing.T) {
	// Set version info for test
	Version = "test-version"
	Commit = "test-commit"
	BuildDate = "test-date"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	http.DefaultServeMux = http.NewServeMux()
	setupRoutes()

	http.DefaultServeMux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	expected := "Meridian vtest-version (commit: test-commit, built: test-date)\n"
	if w.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, w.Body.String())
	}
}
