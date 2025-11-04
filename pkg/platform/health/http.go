package health

import (
	"encoding/json"
	"net/http"
)

// HTTPHandler provides HTTP endpoints for health checks.
type HTTPHandler struct {
	aggregator *Aggregator
}

// NewHTTPHandler creates a new HTTP health check handler.
func NewHTTPHandler(aggregator *Aggregator) *HTTPHandler {
	return &HTTPHandler{
		aggregator: aggregator,
	}
}

// LivenessResponse represents the liveness probe response.
type LivenessResponse struct {
	Status string `json:"status"`
}

// ReadinessResponse represents the readiness probe response with component details.
type ReadinessResponse struct {
	Status     string                `json:"status"`
	Components []ComponentStatusJSON `json:"components"`
}

// ComponentStatusJSON is the JSON representation of a component health check.
type ComponentStatusJSON struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	ResponseTime string `json:"response_time,omitempty"`
	Error        string `json:"error,omitempty"`
}

// LivenessHandler handles liveness probe requests (GET /health/live).
// Returns 200 OK if the service process is running.
// This endpoint always returns healthy unless the process is dead.
func (h *HTTPHandler) LivenessHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := LivenessResponse{
		Status: "alive",
	}

	_ = json.NewEncoder(w).Encode(response)
}

// ReadinessHandler handles readiness probe requests (GET /health/ready).
// Returns 200 OK if all dependencies are healthy.
// Returns 503 Service Unavailable if any dependency is unhealthy.
func (h *HTTPHandler) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	report := h.aggregator.CheckAll(ctx)
	overallStatus := report.OverallStatus()

	components := make([]ComponentStatusJSON, len(report.Components))
	for i, comp := range report.Components {
		components[i] = ComponentStatusJSON{
			Name:         comp.Name,
			Status:       comp.Status.String(),
			Message:      comp.Message,
			ResponseTime: comp.ResponseTime.String(),
		}
		if comp.Error != nil {
			components[i].Error = comp.Error.Error()
		}
	}

	response := ReadinessResponse{
		Status:     overallStatus.String(),
		Components: components,
	}

	w.Header().Set("Content-Type", "application/json")

	if overallStatus == StatusHealthy {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	_ = json.NewEncoder(w).Encode(response)
}

// StartupHandler handles startup probe requests (GET /health/startup).
// Returns 200 OK once the service has completed startup initialization.
// This is currently the same as liveness - can be enhanced to check
// if initialization tasks (database migrations, etc.) are complete.
func (h *HTTPHandler) StartupHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := LivenessResponse{
		Status: "started",
	}

	_ = json.NewEncoder(w).Encode(response)
}

// RegisterHandlers registers all health check endpoints on the provided mux.
func (h *HTTPHandler) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/health/live", h.LivenessHandler)
	mux.HandleFunc("/health/ready", h.ReadinessHandler)
	mux.HandleFunc("/health/startup", h.StartupHandler)
}
