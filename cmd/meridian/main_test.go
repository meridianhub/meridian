package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestHealthEndpoints(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		expectedBody string
	}{
		{"liveness probe", "/health/live", "alive\n"},
		{"readiness probe", "/health/ready", "ready\n"},
		{"startup probe", "/health/startup", "started\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use local mux to avoid global state mutation
			mux := http.NewServeMux()
			setupRoutes(mux)

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}
			if w.Body.String() != tt.expectedBody {
				t.Errorf("Expected body %q, got %q", tt.expectedBody, w.Body.String())
			}
		})
	}
}

func TestRootEndpoint(t *testing.T) {
	// Set version info for test and restore after
	prevVersion, prevCommit, prevBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version, Commit, BuildDate = prevVersion, prevCommit, prevBuildDate
	})
	Version = "test-version"
	Commit = "test-commit"
	BuildDate = "test-date"

	// Use local mux to avoid global state mutation
	mux := http.NewServeMux()
	setupRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	expected := "Meridian vtest-version (commit: test-commit, built: test-date)\n"
	if w.Body.String() != expected {
		t.Errorf("Expected body %q, got %q", expected, w.Body.String())
	}
}

func TestGetPort(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     string
	}{
		{
			name:     "default port when PORT not set",
			envValue: "",
			want:     "8080",
		},
		{
			name:     "custom port from environment",
			envValue: "9090",
			want:     "9090",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original value
			originalPort := os.Getenv("PORT")
			defer func() {
				if originalPort != "" {
					_ = os.Setenv("PORT", originalPort)
				} else {
					_ = os.Unsetenv("PORT")
				}
			}()

			// Set test value
			if tt.envValue == "" {
				_ = os.Unsetenv("PORT")
			} else {
				_ = os.Setenv("PORT", tt.envValue)
			}

			got := getPort()
			if got != tt.want {
				t.Errorf("getPort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateServer(t *testing.T) {
	tests := []struct {
		name string
		port string
		want string
	}{
		{
			name: "standard port",
			port: "8080",
			want: ":8080",
		},
		{
			name: "custom port",
			port: "9090",
			want: ":9090",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := createServer(tt.port)

			if server.Addr != tt.want {
				t.Errorf("createServer().Addr = %q, want %q", server.Addr, tt.want)
			}

			// Verify timeout configuration
			if server.ReadHeaderTimeout != 10*time.Second {
				t.Errorf("ReadHeaderTimeout = %v, want 10s", server.ReadHeaderTimeout)
			}
			if server.ReadTimeout != 30*time.Second {
				t.Errorf("ReadTimeout = %v, want 30s", server.ReadTimeout)
			}
			if server.WriteTimeout != 30*time.Second {
				t.Errorf("WriteTimeout = %v, want 30s", server.WriteTimeout)
			}
			if server.IdleTimeout != 120*time.Second {
				t.Errorf("IdleTimeout = %v, want 120s", server.IdleTimeout)
			}
		})
	}
}
