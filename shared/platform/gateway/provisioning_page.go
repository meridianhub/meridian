package gateway

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
)

// provisioningPageData holds the template data for the provisioning progress page.
type provisioningPageData struct {
	TenantName string
	TenantSlug string
	TenantID   string
	Status     string
	Services   []serviceStatusData
	StatusJSON template.JS
}

// serviceStatusData holds per-service provisioning status for template rendering.
type serviceStatusData struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt,omitempty"`
	Duration  string `json:"duration,omitempty"`
	Error     string `json:"error,omitempty"`
}

// formatServiceName converts internal service names to display names.
// e.g., "party" -> "Party", "reference_data" -> "Reference Data"
func formatServiceName(name string) string {
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

// buildServiceStatusData converts domain provisioning statuses to template data.
func buildServiceStatusData(statuses []domain.ProvisioningStatus) []serviceStatusData {
	result := make([]serviceStatusData, 0, len(statuses))
	for _, s := range statuses {
		sd := serviceStatusData{
			Name:   formatServiceName(s.ServiceName),
			Status: string(s.Status),
		}
		if s.StartedAt != nil {
			sd.StartedAt = s.StartedAt.Format(time.RFC3339)
			if s.CompletedAt != nil {
				sd.Duration = s.CompletedAt.Sub(*s.StartedAt).Round(time.Millisecond).String()
			}
		}
		if s.ErrorMessage != nil {
			sd.Error = "Provisioning failed"
		}
		result = append(result, sd)
	}
	return result
}

// serveProvisioningPage renders the provisioning progress page as an HTTP response.
func serveProvisioningPage(w http.ResponseWriter, data provisioningPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusServiceUnavailable)

	if err := provisioningTemplate.Execute(w, data); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// serveProvisioningStatusJSON renders provisioning status as JSON for polling.
func serveProvisioningStatusJSON(w http.ResponseWriter, tenantStatus string, services []serviceStatusData) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	resp := struct {
		Status   string              `json:"status"`
		Services []serviceStatusData `json:"services"`
	}{
		Status:   tenantStatus,
		Services: services,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// marshalServicesJSON converts service status data to a JSON string for embedding in templates.
func marshalServicesJSON(services []serviceStatusData) template.JS {
	if len(services) == 0 {
		return "[]"
	}
	b, err := json.Marshal(services)
	if err != nil {
		return "[]"
	}
	return template.JS(b) //nolint:gosec // Controlled JSON output, not user input
}

// provisioningTemplate is the compiled HTML template for the provisioning page.
var provisioningTemplate = template.Must(template.New("provisioning").Parse(fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Setting up {{.TenantName}} - Meridian</title>
<style>
%s
</style>
</head>
<body>
<div class="container">
  <div class="logo">
    <svg width="40" height="40" viewBox="0 0 40 40" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="20" cy="20" r="18" stroke="#6366f1" stroke-width="2.5" fill="none"/>
      <ellipse cx="20" cy="20" rx="10" ry="18" stroke="#6366f1" stroke-width="1.5" fill="none" opacity="0.6"/>
      <line x1="2" y1="20" x2="38" y2="20" stroke="#6366f1" stroke-width="1.5" opacity="0.4"/>
    </svg>
  </div>
  <h1>Setting up your workspace</h1>
  <p class="subtitle" id="tenantName">{{.TenantName}}</p>

  <div class="progress-ring-container">
    <svg class="progress-ring" viewBox="0 0 120 120">
      <circle class="progress-ring-bg" cx="60" cy="60" r="52"/>
      <circle class="progress-ring-fill" cx="60" cy="60" r="52" id="progressRing"/>
    </svg>
    <div class="progress-text" id="progressText">0%%</div>
  </div>

  <div class="services" id="services"></div>

  <p class="status-message" id="statusMessage">Initializing services...</p>
</div>

<script>
%s
</script>
</body>
</html>`, provisioningCSS, provisioningJS)))

const provisioningCSS = `
*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
  background: #0f1117;
  color: #e2e8f0;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
}

.container {
  text-align: center;
  max-width: 480px;
  width: 100%;
  padding: 2rem;
}

.logo {
  margin-bottom: 2rem;
  animation: pulse 2s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.6; }
}

h1 {
  font-size: 1.5rem;
  font-weight: 600;
  color: #f1f5f9;
  margin-bottom: 0.5rem;
}

.subtitle {
  font-size: 0.95rem;
  color: #94a3b8;
  margin-bottom: 2.5rem;
}

.progress-ring-container {
  position: relative;
  width: 120px;
  height: 120px;
  margin: 0 auto 2rem;
}

.progress-ring { width: 100%; height: 100%; transform: rotate(-90deg); }

.progress-ring-bg {
  fill: none;
  stroke: #1e293b;
  stroke-width: 6;
}

.progress-ring-fill {
  fill: none;
  stroke: #6366f1;
  stroke-width: 6;
  stroke-linecap: round;
  stroke-dasharray: 326.73;
  stroke-dashoffset: 326.73;
  transition: stroke-dashoffset 0.6s ease;
}

.progress-text {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  font-size: 1.25rem;
  font-weight: 600;
  color: #f1f5f9;
}

.services {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  margin-bottom: 1.5rem;
  text-align: left;
}

.service {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.625rem 1rem;
  background: #1e293b;
  border-radius: 0.5rem;
  font-size: 0.875rem;
  transition: background 0.3s ease;
}

.service.completed { background: #0f2a1f; }
.service.failed { background: #2a0f0f; }

.service-icon {
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.icon-pending { color: #475569; }
.icon-in-progress { color: #6366f1; animation: spin 1s linear infinite; }
.icon-completed { color: #22c55e; }
.icon-failed { color: #ef4444; }

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

.service-name { flex: 1; color: #cbd5e1; }
.service-duration {
  font-size: 0.75rem;
  color: #64748b;
  font-variant-numeric: tabular-nums;
}

.status-message {
  font-size: 0.875rem;
  color: #64748b;
  animation: fadeInOut 2s ease-in-out infinite;
}

@keyframes fadeInOut {
  0%, 100% { opacity: 0.6; }
  50% { opacity: 1; }
}

.redirect-notice {
  margin-top: 1.5rem;
  padding: 1rem;
  background: #0f2a1f;
  border-radius: 0.5rem;
  color: #22c55e;
  font-size: 0.875rem;
  animation: none;
}
`

const provisioningJS = `
(function() {
  var tenantSlug = "{{.TenantSlug}}";
  var initialServices = {{.StatusJSON}};
  var pollInterval = 2000;
  var circumference = 2 * Math.PI * 52;

  var icons = {
    pending: '<svg viewBox="0 0 20 20" fill="currentColor" width="20" height="20"><circle cx="10" cy="10" r="6" fill="none" stroke="currentColor" stroke-width="2"/></svg>',
    in_progress: '<svg viewBox="0 0 20 20" fill="none" width="20" height="20"><circle cx="10" cy="10" r="7" stroke="currentColor" stroke-width="2" stroke-dasharray="12 8" stroke-linecap="round"/></svg>',
    completed: '<svg viewBox="0 0 20 20" fill="none" width="20" height="20"><path d="M6 10l3 3 5-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>',
    failed: '<svg viewBox="0 0 20 20" fill="none" width="20" height="20"><path d="M7 7l6 6M13 7l-6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>'
  };

  function renderServices(services) {
    var container = document.getElementById("services");
    if (!services || services.length === 0) {
      container.innerHTML = "";
      return;
    }
    var html = "";
    for (var i = 0; i < services.length; i++) {
      var s = services[i];
      var statusClass = s.status || "pending";
      html += '<div class="service ' + statusClass + '">';
      html += '<div class="service-icon icon-' + statusClass + '">' + (icons[statusClass] || icons.pending) + '</div>';
      html += '<span class="service-name">' + escapeHtml(s.name) + '</span>';
      if (s.duration) {
        html += '<span class="service-duration">' + escapeHtml(s.duration) + '</span>';
      }
      html += '</div>';
    }
    container.innerHTML = html;
  }

  function updateProgress(services) {
    if (!services || services.length === 0) return;
    var completed = 0;
    for (var i = 0; i < services.length; i++) {
      if (services[i].status === "completed") completed++;
    }
    var pct = Math.round((completed / services.length) * 100);
    var offset = circumference - (pct / 100) * circumference;
    document.getElementById("progressRing").style.strokeDashoffset = offset;
    document.getElementById("progressText").textContent = pct + "%";
  }

  function updateStatusMessage(status) {
    var msg = document.getElementById("statusMessage");
    switch (status) {
      case "provisioning_pending":
        msg.textContent = "Queued for setup...";
        break;
      case "provisioning":
        msg.textContent = "Creating your workspace...";
        break;
      case "active":
        msg.className = "status-message redirect-notice";
        msg.textContent = "Ready! Redirecting...";
        msg.style.animation = "none";
        break;
      case "provisioning_failed":
        msg.textContent = "Setup encountered an error. Our team has been notified.";
        msg.style.animation = "none";
        msg.style.color = "#ef4444";
        break;
      default:
        msg.textContent = "Initializing services...";
    }
  }

  function escapeHtml(str) {
    var div = document.createElement("div");
    div.appendChild(document.createTextNode(str));
    return div.innerHTML;
  }

  function poll() {
    fetch("/api/provisioning-status")
      .then(function(r) { return r.json(); })
      .then(function(data) {
        if (data.status === "active") {
          renderServices(data.services);
          updateProgress(data.services);
          updateStatusMessage("active");
          var ring = document.getElementById("progressRing");
          ring.style.strokeDashoffset = "0";
          document.getElementById("progressText").textContent = "100%";
          setTimeout(function() { window.location.reload(); }, 1500);
          return;
        }
        if (data.services) {
          renderServices(data.services);
          updateProgress(data.services);
        }
        updateStatusMessage(data.status);
        setTimeout(poll, pollInterval);
      })
      .catch(function() {
        setTimeout(poll, pollInterval);
      });
  }

  // Initial render from server-embedded data
  renderServices(initialServices);
  updateProgress(initialServices);
  updateStatusMessage("{{.Status}}");

  // Start polling
  setTimeout(poll, pollInterval);
})();
`
